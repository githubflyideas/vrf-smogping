package main

import (
	"context"
	"errors"
	"net"
	"runtime"
	"testing"
	"time"
)

func TestScopedDNSTransportBindsSource(t *testing.T) {
	for _, network := range []string{"udp4", "tcp4"} {
		t.Run(network, func(t *testing.T) {
			var address string
			if network == "udp4" {
				s, e := net.ListenPacket(network, "127.0.0.1:0")
				if e != nil {
					t.Fatal(e)
				}
				defer s.Close()
				address = s.LocalAddr().String()
			} else {
				s, e := net.Listen(network, "127.0.0.1:0")
				if e != nil {
					t.Fatal(e)
				}
				defer s.Close()
				address = s.Addr().String()
			}
			source := net.ParseIP("127.0.0.1")
			resolver := targetResolver(TargetCfg{SourceAddress: source.String()}, source, time.Second)
			c, e := resolver.Dial(context.Background(), network, address)
			if e != nil {
				t.Fatal(e)
			}
			host, _, _ := net.SplitHostPort(c.LocalAddr().String())
			c.Close()
			if host != source.String() {
				t.Fatalf("DNS source %s", host)
			}
			resolver = targetResolver(TargetCfg{VRF: "missing-vrf"}, nil, time.Second)
			c, e = resolver.Dial(context.Background(), network, address)
			if e == nil {
				c.Close()
				t.Fatal("scoped DNS fell back to default route")
			}
		})
	}
}

func TestDNSDeadline(t *testing.T) {
	old := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	defer func() { net.DefaultResolver = old }()
	start := time.Now()
	r, e := tcpRoundTarget(TargetCfg{Type: "tcp", Host: "deadline.test.invalid", Port: 80}, 1, 0, 30*time.Millisecond)
	if e == nil || r.S != 0 || time.Since(start) > time.Second {
		t.Fatalf("unbounded DNS: %+v %v elapsed %v", r, e, time.Since(start))
	}
}

func TestTCPIPv6Source(t *testing.T) {
	ln, e := net.Listen("tcp6", "[::1]:0")
	if e != nil {
		t.Skipf("IPv6 unavailable: %v", e)
	}
	defer ln.Close()
	go func() {
		c, e := ln.Accept()
		if e == nil {
			c.Close()
		}
	}()
	r, e := tcpRoundTarget(TargetCfg{Type: "tcp", Host: "::1", Port: ln.Addr().(*net.TCPAddr).Port, SourceAddress: "::1"}, 1, 0, time.Second)
	if e != nil || r.R != 1 || r.S != 1 {
		t.Fatalf("IPv6 source: %+v %v", r, e)
	}
}

func TestStoppedProbeDoesNotWrite(t *testing.T) {
	target := TargetCfg{Name: "stopped", Type: "tcp", Host: "127.0.0.1", Port: 1, dir: "stopped"}
	s, err := NewStore(t.TempDir(), []TargetCfg{target})
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	close(stop)
	probeLoop(target, ProbeCfg{IntervalSec: 1, Packets: 1, TimeoutMs: 10}, s, NewDetector(s), stop)
	if got := s.Recent(target.Name, 0); len(got) != 0 {
		t.Fatalf("stopped probe wrote %+v", got)
	}
}

func TestLoopUsesTargetRouting(t *testing.T) {
	target := TargetCfg{Name: "bad-route", Type: "tcp", Host: "127.0.0.1", Port: 1, VRF: "missing-vrf", dir: "bad-route"}
	s, err := NewStore(t.TempDir(), []TargetCfg{target})
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		probeLoop(target, ProbeCfg{IntervalSec: 1, Packets: 1, TimeoutMs: 10}, s, NewDetector(s), stop)
	}()
	defer func() { close(stop); <-done }()
	// This checks routing and persistence, not disk or scheduler latency.
	until := time.Now().Add(5 * time.Second)
	for time.Now().Before(until) {
		if rounds := s.Recent(target.Name, 0); len(rounds) > 0 {
			r := rounds[0]
			if r.Error == "" || r.S != 0 || r.R != 0 || r.B {
				t.Fatalf("routing ignored: %+v", r)
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no probe round")
}

func TestICMPRoutingErrors(t *testing.T) {
	for _, target := range []TargetCfg{
		{Type: "icmp", Host: "127.0.0.1", SourceAddress: "192.0.2.254"},
		{Type: "icmp", Host: "127.0.0.1", VRF: "missing-vrf"},
		{Type: "icmp", Host: "::1"},
	} {
		r, err := icmpRoundTarget(target, 2, 0, 50*time.Millisecond)
		if err == nil || r.Error == "" || r.S != 0 || r.R != 0 {
			t.Fatalf("bad ICMP context: %+v %v", r, err)
		}
	}
	if runtime.GOOS == "windows" {
		r, err := icmpRound("127.0.0.1", 1, 0, time.Millisecond)
		if err == nil || r.Error == "" || r.S != 0 {
			t.Fatalf("unsupported ICMP: %+v %v", r, err)
		}
	}
}

func TestDNSFailureIsNotPacketLoss(t *testing.T) {
	old := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		return nil, errors.New("test DNS unavailable")
	}}
	defer func() { net.DefaultResolver = old }()
	r, err := tcpRoundTarget(TargetCfg{Type: "tcp", Host: "probe.test.invalid", Port: 80}, 2, 0, 50*time.Millisecond)
	if err == nil || r.Error == "" || r.S != 0 || r.R != 0 {
		t.Fatalf("DNS failure became loss: %+v %v", r, err)
	}
}

func TestInvalidVRFNeverFallsBack(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	for _, vrf := range []string{"missing-vrf", "lo"} {
		r, err := tcpRoundTarget(TargetCfg{Type: "tcp", Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, VRF: vrf}, 1, 0, 100*time.Millisecond)
		if err == nil || r.Error == "" || r.S != 0 || r.R != 0 {
			t.Fatalf("VRF %s fell back: %+v %v", vrf, r, err)
		}
	}
}

func TestTCPExplicitSource(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
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
	target := TargetCfg{Type: "tcp", Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, SourceAddress: "127.0.0.1"}
	r, err := tcpRoundTarget(target, 2, time.Millisecond, time.Second)
	if err != nil || r.S != 2 || r.R != 2 || len(r.MS) != 2 {
		t.Fatalf("%+v %v", r, err)
	}
	for i := 0; i < 2; i++ {
		select {
		case ip := <-peers:
			if ip != "127.0.0.1" {
				t.Fatal(ip)
			}
		case <-time.After(time.Second):
			t.Fatal("no connection")
		}
	}
	target.SourceAddress = "192.0.2.254"
	r, err = tcpRoundTarget(target, 2, 0, time.Second)
	if err == nil || r.Error == "" || r.S != 0 || r.R != 0 {
		t.Fatalf("invalid local source became loss: %+v %v", r, err)
	}
}
