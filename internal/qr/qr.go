// Package qr works out the address a phone should use and renders it as a QR
// code, so nobody has to read an IP off a screen and type it on a handset.
package qr

import (
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strconv"

	qrcode "github.com/skip2/go-qrcode"
)

// PNG renders the URL at the given pixel size.
func PNG(url string, size int) ([]byte, error) {
	return qrcode.Encode(url, qrcode.Medium, size)
}

// Terminal renders the URL as half-block text, small enough to fit a normal
// terminal and still scan. Useful on a headless box where no window can open.
func Terminal(url string) (string, error) {
	code, err := qrcode.New(url, qrcode.Low)
	if err != nil {
		return "", err
	}
	// Inverted: terminals are usually dark, and the scanner needs the quiet
	// zone to read light while the modules read dark.
	return code.ToSmallString(true), nil
}

/*
PrimaryIP returns the address a phone on the LAN should connect to.

Enumerating interfaces and picking the first non-loopback answer goes wrong on
any machine with Docker, VPNs, or virtual bridges - this host alone has three
candidates. Opening a UDP socket toward a public address makes the routing
table choose for us, and because UDP is connectionless no packet is actually
sent.
*/
func PrimaryIP() (string, error) {
	conn, err := net.Dial("udp", "8.8.8.8:80")
	if err != nil {
		return fallbackIP()
	}
	defer conn.Close()

	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok || addr.IP == nil {
		return fallbackIP()
	}
	return addr.IP.String(), nil
}

// fallbackIP is for a host with no default route at all, such as an isolated
// switch with only static addressing.
func fallbackIP() (string, error) {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", err
	}
	for _, a := range addrs {
		ipnet, ok := a.(*net.IPNet)
		if !ok || ipnet.IP.IsLoopback() || ipnet.IP.To4() == nil {
			continue
		}
		return ipnet.IP.String(), nil
	}
	return "", fmt.Errorf("no usable IPv4 address found")
}

// URL is the address to hand out, given the port obs-deck is serving on.
func URL(port int) (string, error) {
	ip, err := PrimaryIP()
	if err != nil {
		return "", err
	}
	return "http://" + net.JoinHostPort(ip, strconv.Itoa(port)), nil
}

/*
OpenBrowser asks the desktop to open a URL.

This is how the "window" is provided without linking a GUI toolkit: a native
window would mean cgo plus X11 or OpenGL at runtime, which would cost the
single static binary that makes this thing easy to deploy. The browser is
already a window, and it is already installed.
*/
func OpenBrowser(url string) error {
	var cmd string
	var args []string

	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler"}
	default:
		cmd = "xdg-open"
	}
	args = append(args, url)
	return exec.Command(cmd, args...).Start()
}
