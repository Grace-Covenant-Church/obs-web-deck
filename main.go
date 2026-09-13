// Command obs-deck serves a phone-friendly Stream Deck style control surface
// for OBS, with a low-latency WebRTC preview of the program output.
//
// Everything ships in one static binary: the UI assets, the WHIP/WHEP media
// server, and the obs-websocket client.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/Grace-Covenant-Church/obs-web-deck/internal/config"
	"github.com/Grace-Covenant-Church/obs-web-deck/internal/media"
	"github.com/Grace-Covenant-Church/obs-web-deck/internal/obsws"
	"github.com/Grace-Covenant-Church/obs-web-deck/internal/ptz"
	"github.com/Grace-Covenant-Church/obs-web-deck/internal/qr"
	"github.com/Grace-Covenant-Church/obs-web-deck/internal/server"
)

// version is stamped by the release build via -ldflags "-X main.version=...".
var version = "dev"

//go:embed all:web
var webAssets embed.FS

// Defaults are embedded so a fresh install can materialise a working config
// on first run; the binary stays self-contained but the files remain editable.
//
//go:embed config/settings.json config/deck.json
var defaultConfig embed.FS

func main() {
	showVersion := flag.Bool("version", false, "print the version and exit")
	configDir := flag.String("config", "config", "directory holding settings.json and deck.json")
	addr := flag.String("addr", "", "override listen address, e.g. 0.0.0.0:8080")
	noWindow := flag.Bool("no-window", false, "do not open the QR code in a browser window")
	noQR := flag.Bool("no-qr", false, "do not print the QR code to the terminal")
	flag.Parse()

	if *showVersion {
		fmt.Println("obs-web-deck", version)
		return
	}
	log.Printf("obs-web-deck %s", version)

	if err := ensureConfig(*configDir); err != nil {
		log.Fatalf("config: %v", err)
	}

	store, err := config.NewStore(*configDir)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	web, err := fs.Sub(webAssets, "web")
	if err != nil {
		log.Fatalf("assets: %v", err)
	}

	mediaSrv, err := media.NewServer()
	if err != nil {
		log.Fatalf("media: %v", err)
	}

	var srv *server.Server
	obs := obsws.New(store, func(st obsws.State) {
		if srv != nil {
			srv.BroadcastState(st)
		}
	})
	pan := ptz.New(obs, store.Settings)
	srv = server.New(store, obs, pan, mediaSrv, web)

	go obs.Run()

	ctx, cancelIngest := context.WithCancel(context.Background())
	defer cancelIngest()

	if set := store.Settings(); set.Video.Mode == "mpegts" {
		if err := mediaSrv.StartMPEGTS(ctx, set.Video.TSListen, set.Video.StreamPath, set.Video.MaxLag()); err != nil {
			log.Fatalf("mpegts: %v", err)
		}
	}

	listen := *addr
	if listen == "" {
		set := store.Settings()
		listen = net.JoinHostPort(set.Server.BindAddress, strconv.Itoa(set.Server.Port))
	}

	httpSrv := &http.Server{
		Addr:              listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		set := store.Settings()
		log.Printf("obs-deck listening on http://%s", listen)
		for _, ip := range localIPs() {
			log.Printf("  phone:  http://%s:%d", ip, set.Server.Port)
		}
		if set.Video.Mode == "builtin" {
			log.Printf("  OBS WHIP URL:  http://127.0.0.1:%d/%s/whip",
				set.Server.Port, set.Video.StreamPath)
		}
		for _, u := range tsPushURLs(set.Video) {
			log.Printf("  OBS MPEG-TS URL:  %s", u)
		}
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("listen: %v", err)
		}
	}()

	closeWindow := announce(srv, *noQR, *noWindow)

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig

	log.Println("shutting down")
	closeWindow()
	// Halt the camera before dropping the OBS connection, or it keeps slewing.
	_ = pan.Stop()
	obs.Close()
	_ = httpSrv.Close()
}

/*
announce prints the join address as a scannable QR and, on a desktop, opens it
as a badge in the corner of the screen. The returned function closes that badge
again, and is always safe to call.

The badge is a browser window rather than a native one on purpose: a GUI
toolkit would pull in cgo and X11/OpenGL at runtime and cost the single static
binary. The terminal code covers headless machines, where nothing can open
anyway.
*/
func announce(srv *server.Server, noQR, noWindow bool) (closeWindow func()) {
	noop := func() {}

	url, err := srv.JoinURL()
	if err != nil {
		log.Printf("[qr] could not determine LAN address: %v", err)
		return noop
	}

	if !noQR {
		if art, err := qr.Terminal(url); err == nil {
			fmt.Printf("\n%s\n  Scan to open the deck:  %s\n\n", art, url)
		}
	}

	if noWindow || !hasDisplay() {
		return noop
	}
	stop, err := qr.OpenWindow(url + "/qr")
	if err != nil {
		log.Printf("[qr] could not open a window (%v); browse to %s/qr", err, url)
		return noop
	}
	return stop
}

// hasDisplay reports whether there is a desktop session to open a window in.
// Without this, a systemd service or an SSH session would log a spurious
// failure from xdg-open on every start.
func hasDisplay() bool {
	if runtime.GOOS != "linux" {
		return true
	}
	return os.Getenv("DISPLAY") != "" || os.Getenv("WAYLAND_DISPLAY") != ""
}

// ensureConfig writes the embedded defaults on first run, without ever
// overwriting files the operator has already edited.
func ensureConfig(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, name := range []string{"settings.json", "deck.json"} {
		dst := filepath.Join(dir, name)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		blob, err := defaultConfig.ReadFile("config/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(dst, blob, 0o644); err != nil {
			return err
		}
		log.Printf("[config] wrote default %s", dst)
	}
	return nil
}

/*
tsPushURLs lists the addresses OBS can push the preview to.

A loopback bind only accepts a push from this machine, and there is exactly one
URL to print. Bound wider it accepts one from anywhere on the LAN, and the
operator then has to know which of this machine's addresses the OBS machine can
actually see - which is worth printing rather than making them work out.
*/
func tsPushURLs(v config.VideoSettings) []string {
	if v.Mode != "mpegts" {
		return nil
	}
	host, port, err := net.SplitHostPort(v.TSListen)
	if err != nil {
		return nil
	}

	hosts := []string{host}
	if host == "" || host == "0.0.0.0" || host == "::" {
		hosts = localIPs()
	}

	out := make([]string, 0, len(hosts))
	for _, h := range hosts {
		// pkt_size matches what the README tells OBS to use: one TS packet
		// short of a 1500-byte MTU, so nothing fragments on the way in.
		out = append(out, fmt.Sprintf("udp://%s:%s?pkt_size=1316", h, port))
	}
	return out
}

// localIPs lists the LAN addresses a phone could reach, so the operator does
// not have to go looking for the machine's IP.
func localIPs() []string {
	var out []string
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return out
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.To4() == nil {
			continue
		}
		out = append(out, ipnet.IP.String())
	}
	return out
}
