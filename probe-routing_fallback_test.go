package main

import (
	"context"
	"encoding/binary"
	"net"
	"strconv"
	"testing"
	"time"
)

// A real loopback DNS transport supplies a deterministic ordered RRset without
// public DNS or changes to hosts files. Only the resolver transport is replaced.
func TestTCPHostnameFallsBackToSecondAddress(t *testing.T) {
	dns, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer dns.Close()
	go func() {
		b := make([]byte, 512)
		for {
			n, peer, err := dns.ReadFrom(b)
			if err != nil {
				return
			}
			if n < 17 {
				continue
			}
			end := 12
			for end < n && b[end] != 0 {
				end += int(b[end]) + 1
			}
			end += 5
			if end > n {
				continue
			}
			response := append([]byte(nil), b[:end]...)
			response[2], response[3] = 0x81, 0x80
			response[6], response[7], response[10], response[11] = 0, 0, 0, 0
			if binary.BigEndian.Uint16(b[end-4:end-2]) == 1 {
				response[7] = 2
				for _, last := range []byte{2, 1} {
					response = append(response, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 0, 0, 4, 127, 0, 0, last)
				}
			}
			dns.WriteTo(response, peer)
		}
	}()
	old := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "udp4", dns.LocalAddr().String())
	}}
	defer func() { net.DefaultResolver = old }()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	target := TargetCfg{Type: "tcp", Host: "fallback.test.invalid", Port: ln.Addr().(*net.TCPAddr).Port}
	first, err := resolveTarget(target, nil, time.Second)
	if err != nil || first != "127.0.0.2" {
		t.Fatalf("fixture first address %q: %v", first, err)
	}
	// Prove the first endpoint really is unavailable, not merely ordered first.
	c, err := net.DialTimeout("tcp4", net.JoinHostPort(first, strconv.Itoa(target.Port)), time.Second)
	if err == nil {
		c.Close()
		t.Fatal("fixture first address accepts connections")
	}
	r, err := tcpRoundTarget(target, 1, 0, 3*time.Second)
	if err != nil || r.Error != "" || r.S != 1 || r.R != 1 {
		t.Fatalf("hostname fallback: %+v %v", r, err)
	}
}
