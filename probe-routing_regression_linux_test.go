//go:build linux

package main

import (
	"errors"
	"net"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// Exhaust descriptors only in a subprocess, never in the test runner.
func TestTCPSocketExhaustionIsError(t *testing.T) {
	if os.Getenv("JSON_PING_EMFILE_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestTCPSocketExhaustionIsError$", "-test.v")
		cmd.Env = append(os.Environ(), "JSON_PING_EMFILE_CHILD=1")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("child: %v\n%s", err, out)
		}
		return
	}
	// Initialize Go's process-wide poller before descriptor exhaustion. Otherwise
	// the fixture crashes in netpollinit instead of testing socket(2) error handling.
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listener.Close()
	timer := time.NewTimer(time.Millisecond)
	<-timer.C
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		t.Fatal(err)
	}
	lim.Cur = 64
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		t.Fatal(err)
	}
	var fds []int
	defer func() {
		for _, fd := range fds {
			syscall.Close(fd)
		}
	}()
	for {
		fd, err := syscall.Open("/dev/null", syscall.O_RDONLY, 0)
		if errors.Is(err, syscall.EMFILE) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		fds = append(fds, fd)
	}
	r, err := tcpRoundTarget(TargetCfg{Type: "tcp", Host: "127.0.0.1", Port: 1}, 2, 0, time.Second)
	if !errors.Is(err, syscall.EMFILE) || r.Error == "" || r.S != 0 || r.R != 0 {
		t.Fatalf("socket exhaustion became loss: %+v %v", r, err)
	}
}

// Disposable Linux fixture: add an /etc/hosts name with 127.0.0.2 first,
// 127.0.0.1 second and pass JSON_PING_TEST_MULTI_HOST. Source constraints
// still apply to every fallback attempt; no public DNS is used.
func TestTCPScopedHostnameFallback(t *testing.T) {
	host := os.Getenv("JSON_PING_TEST_MULTI_HOST")
	if host == "" {
		t.Skip("requires disposable hosts fixture JSON_PING_TEST_MULTI_HOST")
	}
	target := TargetCfg{Type: "tcp", Host: host, SourceAddress: "127.0.0.1"}
	first, err := resolveTarget(target, net.ParseIP(target.SourceAddress), time.Second)
	if err != nil || first != "127.0.0.2" {
		t.Fatalf("fixture first address %q: %v", first, err)
	}
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	target.Port = ln.Addr().(*net.TCPAddr).Port
	r, err := tcpRoundTarget(target, 1, 0, time.Second)
	if err != nil || r.Error != "" || r.S != 1 || r.R != 1 {
		t.Fatalf("scoped fallback: %+v %v", r, err)
	}
	ln.(*net.TCPListener).SetDeadline(time.Now().Add(time.Second))
	c, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := c.RemoteAddr().(*net.TCPAddr).IP.String(); got != target.SourceAddress {
		t.Fatalf("fallback source %s", got)
	}
}
