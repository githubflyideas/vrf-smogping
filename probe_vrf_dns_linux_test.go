//go:build linux

package main

import (
	"context"
	"encoding/binary"
	"io"
	"net"
	"os"
	"sync/atomic"
	"testing"
	"time"
)

func TestLinuxVRFScopedDNSFallback(t *testing.T) {
	vrf, source := os.Getenv("JSON_PING_TEST_VRF"), os.Getenv("JSON_PING_TEST_SOURCE")
	if vrf == "" || source == "" {
		t.Skip("requires disposable VRF fixture")
	}
	lc := net.ListenConfig{Control: routingControl(vrf)}
	tcp, err := lc.Listen(context.Background(), "tcp4", net.JoinHostPort(source, "0"))
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	udp, err := lc.ListenPacket(context.Background(), "udp4", tcp.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	var udpSeen, tcpSeen atomic.Int32
	errors := make(chan error, 2)
	response := func(q []byte, truncated bool) []byte {
		if len(q) < 12 {
			return nil
		}
		end := 12
		for end < len(q) && q[end] != 0 {
			end += int(q[end]) + 1
		}
		end += 5
		if end > len(q) {
			return nil
		}
		out := append([]byte(nil), q[:end]...)
		binary.BigEndian.PutUint16(out[2:4], 0x8180)
		binary.BigEndian.PutUint16(out[4:6], 1)
		for i := 6; i < 12; i++ {
			out[i] = 0
		}
		if truncated {
			binary.BigEndian.PutUint16(out[2:4], 0x8380)
			return out
		}
		binary.BigEndian.PutUint16(out[6:8], 1)
		return append(out, 0xc0, 0x0c, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 192, 0, 2, 99)
	}
	go func() {
		buf := make([]byte, 1500)
		udp.SetDeadline(time.Now().Add(5 * time.Second))
		n, peer, e := udp.ReadFrom(buf)
		if e != nil {
			errors <- e
			return
		}
		if peer.(*net.UDPAddr).IP.String() != source {
			errors <- &net.AddrError{Err: "incorrect DNS UDP source", Addr: peer.String()}
			return
		}
		udpSeen.Add(1)
		_, e = udp.WriteTo(response(buf[:n], true), peer)
		errors <- e
	}()
	go func() {
		tcp.(*net.TCPListener).SetDeadline(time.Now().Add(5 * time.Second))
		conn, e := tcp.Accept()
		if e != nil {
			errors <- e
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(5 * time.Second))
		if conn.RemoteAddr().(*net.TCPAddr).IP.String() != source {
			errors <- &net.AddrError{Err: "incorrect DNS TCP source", Addr: conn.RemoteAddr().String()}
			return
		}
		var prefix [2]byte
		if _, e = io.ReadFull(conn, prefix[:]); e != nil {
			errors <- e
			return
		}
		q := make([]byte, binary.BigEndian.Uint16(prefix[:]))
		if _, e = io.ReadFull(conn, q); e != nil {
			errors <- e
			return
		}
		tcpSeen.Add(1)
		answer := response(q, false)
		binary.BigEndian.PutUint16(prefix[:], uint16(len(answer)))
		_, e = conn.Write(append(prefix[:], answer...))
		errors <- e
	}()
	resolver := targetResolver(TargetCfg{VRF: vrf, SourceAddress: source}, net.ParseIP(source), 2*time.Second)
	dial := resolver.Dial
	// Redirect only the fixture server address; preserve production socket binding.
	resolver.Dial = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dial(ctx, network, tcp.Addr().String())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	ips, err := resolver.LookupIP(ctx, "ip4", "vrf-fixture.test.")
	if err != nil || len(ips) != 1 || ips[0].String() != "192.0.2.99" {
		t.Fatalf("scoped lookup: %v %v", ips, err)
	}
	if udpSeen.Load() != 1 || tcpSeen.Load() != 1 {
		t.Fatalf("DNS transport counts UDP=%d TCP=%d", udpSeen.Load(), tcpSeen.Load())
	}
	for i := 0; i < 2; i++ {
		if err := <-errors; err != nil {
			t.Fatal(err)
		}
	}
}
