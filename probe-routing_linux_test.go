//go:build linux

package main

import (
	"context"
	"net"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// The harness provisions a disposable real VRF and its assigned source. No
// shell commands or host routing changes are performed by the probe or tests.
func TestLinuxVRFIntegration(t *testing.T) {
	vrf, source := os.Getenv("JSON_PING_TEST_VRF"), os.Getenv("JSON_PING_TEST_SOURCE")
	if vrf == "" || source == "" {
		t.Skip("requires JSON_PING_TEST_VRF and JSON_PING_TEST_SOURCE in a disposable Linux VRF fixture")
	}
	if e := validateVRF(vrf); e != nil {
		t.Fatal(e)
	}
	cfg := net.ListenConfig{Control: routingControl(vrf)}
	ln, e := cfg.Listen(context.Background(), "tcp4", net.JoinHostPort(source, "0"))
	if e != nil {
		t.Fatal(e)
	}
	defer ln.Close()
	peers := make(chan string, 2)
	go func() {
		for {
			c, e := ln.Accept()
			if e != nil {
				return
			}
			peers <- c.RemoteAddr().(*net.TCPAddr).IP.String()
			c.Close()
		}
	}()
	target := TargetCfg{Type: "tcp", VRF: vrf, SourceAddress: source, Host: source, Port: ln.Addr().(*net.TCPAddr).Port}
	r, e := tcpRoundTarget(target, 2, time.Millisecond, time.Second)
	if e != nil || r.S != 2 || r.R != 2 || len(r.MS) != 2 {
		t.Fatalf("VRF TCP %+v %v", r, e)
	}
	for i := 0; i < 2; i++ {
		select {
		case peer := <-peers:
			if peer != source {
				t.Fatalf("source %s", peer)
			}
		case <-time.After(time.Second):
			t.Fatal("no accepted peer")
		}
	}
	target.Type = "icmp"
	r, e = icmpRoundTarget(target, 2, 10*time.Millisecond, 200*time.Millisecond)
	if e != nil || r.S != 2 || r.R != 2 || len(r.MS) != 2 {
		t.Fatalf("VRF ICMP %+v %v", r, e)
	}
	// Inspect the actual DNS socket; a resolver config alone is not evidence.
	dns, e := cfg.ListenPacket(context.Background(), "udp4", net.JoinHostPort(source, "0"))
	if e != nil {
		t.Fatal(e)
	}
	defer dns.Close()
	c, e := targetResolver(target, net.ParseIP(source), time.Second).Dial(context.Background(), "udp4", dns.LocalAddr().String())
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	raw, e := c.(*net.UDPConn).SyscallConn()
	if e != nil {
		t.Fatal(e)
	}
	var device string
	var sockErr error
	if e = raw.Control(func(fd uintptr) {
		var buf [16]byte
		n := uint32(len(buf))
		_, _, errno := syscall.Syscall6(syscall.SYS_GETSOCKOPT, fd, syscall.SOL_SOCKET, syscall.SO_BINDTODEVICE, uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&n)), 0)
		if errno != 0 {
			sockErr = errno
		}
		device = strings.TrimRight(string(buf[:]), "\x00")
	}); e != nil {
		t.Fatal(e)
	}
	if sockErr != nil || device != vrf {
		t.Fatalf("DNS bound device %q: %v", device, sockErr)
	}
}
