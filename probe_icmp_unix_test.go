//go:build linux || darwin || freebsd || openbsd || netbsd || dragonfly

package main

import (
	"syscall"
	"testing"
	"time"
)

// Needs ICMP permission (ping_group_range or CAP_NET_RAW); skipped without it.
func TestICMPRoundLoopback(t *testing.T) {
	fd, _, err := icmpSocket()
	if err != nil {
		t.Skipf("no ICMP permission here: %v", err)
	}
	syscall.Close(fd)
	r, err := icmpRound("127.0.0.1", 3, 10*time.Millisecond, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if r.S != 3 || r.R != 3 || len(r.MS) != 3 {
		t.Fatalf("loopback ping: %+v", r)
	}
}
