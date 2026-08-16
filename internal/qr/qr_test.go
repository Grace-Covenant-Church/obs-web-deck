package qr

import (
	"bytes"
	"image/png"
	"net"
	"strings"
	"testing"
)

func TestPNGIsDecodableImage(t *testing.T) {
	blob, err := PNG("http://192.168.1.50:8080", 256)
	if err != nil {
		t.Fatalf("PNG: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(blob))
	if err != nil {
		t.Fatalf("not a valid PNG: %v", err)
	}
	if b := img.Bounds(); b.Dx() < 128 || b.Dy() < 128 {
		t.Fatalf("unexpectedly small image: %v", b)
	}
}

func TestTerminalRendersBlocks(t *testing.T) {
	art, err := Terminal("http://192.168.1.50:8080")
	if err != nil {
		t.Fatalf("Terminal: %v", err)
	}
	// Half-block rendering is what keeps the code small enough to fit a normal
	// terminal window and still scan.
	if !strings.ContainsAny(art, "█▀▄") {
		t.Fatalf("expected block characters, got:\n%s", art)
	}
	if strings.Count(art, "\n") < 10 {
		t.Fatalf("suspiciously short QR:\n%s", art)
	}
}

// PrimaryIP must return a routable LAN address, never loopback: handing a
// phone 127.0.0.1 would be useless, and it is the obvious failure mode if the
// interface enumeration is wrong.
func TestPrimaryIPIsRoutable(t *testing.T) {
	ip, err := PrimaryIP()
	if err != nil {
		t.Skipf("no network available: %v", err)
	}
	parsed := net.ParseIP(ip)
	if parsed == nil {
		t.Fatalf("not an IP: %q", ip)
	}
	if parsed.IsLoopback() {
		t.Fatalf("returned loopback %q; a phone cannot reach that", ip)
	}
	if parsed.To4() == nil {
		t.Fatalf("expected IPv4 for a QR target, got %q", ip)
	}
}

func TestURLIncludesPort(t *testing.T) {
	got, err := URL(8080)
	if err != nil {
		t.Skipf("no network available: %v", err)
	}
	if !strings.HasPrefix(got, "http://") || !strings.HasSuffix(got, ":8080") {
		t.Fatalf("malformed join URL: %q", got)
	}
}
