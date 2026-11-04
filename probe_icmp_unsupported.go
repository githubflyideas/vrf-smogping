//go:build !linux && !darwin && !freebsd && !openbsd && !netbsd && !dragonfly

package main

import (
	"fmt"
	"runtime"
	"time"
)

func icmpRound(host string, packets int, gap, timeout time.Duration) (Round, error) {
	return icmpRoundTarget(TargetCfg{Type: "icmp", Host: host}, packets, gap, timeout)
}
func icmpRoundTarget(t TargetCfg, packets int, gap, timeout time.Duration) (Round, error) {
	return probeError(fmt.Errorf("ICMP probing unsupported on %s", runtime.GOOS))
}
