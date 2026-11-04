package main

import (
	"net"
	"os"
	"syscall"
	"testing"
)

func TestTCPSetupErrorClassification(t *testing.T) {
	for _, tc := range []struct {
		name, call string
		errno      error
		want       bool
	}{
		{"socket exhaustion", "socket", syscall.EMFILE, true},
		{"system exhaustion", "socket", syscall.ENFILE, true},
		{"socket memory", "socket", syscall.ENOBUFS, true},
		{"socket permission", "socket", syscall.EACCES, true},
		{"socket initialization", "setsockopt", syscall.EINVAL, true},
		{"local bind", "bind", syscall.EADDRNOTAVAIL, true},
		{"connect permission", "connect", syscall.EACCES, true},
		{"connect forbidden", "connect", syscall.EPERM, true},
		{"connect exhaustion", "connect", syscall.ENOBUFS, true},
		{"refused", "connect", syscall.ECONNREFUSED, false},
		{"timeout", "connect", syscall.ETIMEDOUT, false},
		{"unreachable", "connect", syscall.ENETUNREACH, false},
		{"host unreachable", "connect", syscall.EHOSTUNREACH, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := &net.OpError{Op: "dial", Net: "tcp", Err: &os.SyscallError{Syscall: tc.call, Err: tc.errno}}
			if got := tcpProbeSetupError(err); got != tc.want {
				t.Fatalf("setup error = %v, want %v: %v", got, tc.want, err)
			}
		})
	}
	if tcpProbeSetupError(nil) {
		t.Fatal("successful dial classified as error")
	}
}
