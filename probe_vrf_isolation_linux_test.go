//go:build linux

package main

import (
	"os"
	"testing"
	"time"
)

func TestLinuxVRFIsolation(t *testing.T) {
	blue, red, destination := os.Getenv("JSON_PING_TEST_VRF"), os.Getenv("JSON_PING_TEST_OTHER_VRF"), os.Getenv("JSON_PING_TEST_SOURCE")
	if blue == "" || red == "" || destination == "" {
		t.Skip("requires disposable two-VRF fixture")
	}
	// Same target, different route domains. No explicit source is required.
	target := TargetCfg{Type: "icmp", Host: destination, VRF: blue}
	good, err := icmpRoundTarget(target, 2, 10*time.Millisecond, 200*time.Millisecond)
	if err != nil || good.R != 2 || len(good.MS) != 2 {
		t.Fatalf("VRF-only success: %+v %v", good, err)
	}
	target.VRF = red
	isolated, err := icmpRoundTarget(target, 2, 10*time.Millisecond, 200*time.Millisecond)
	if err != nil || isolated.S != 2 || isolated.R != 0 || isolated.Error != "" {
		t.Fatalf("other VRF must not reach blue local target: %+v %v", isolated, err)
	}
	// An address belonging only to the other routing domain is not a valid
	// explicit source for this VRF, even though it exists on the same machine.
	if otherSource := os.Getenv("JSON_PING_TEST_OTHER_SOURCE"); otherSource != "" {
		target.VRF, target.SourceAddress = blue, otherSource
		wrongSource, err := icmpRoundTarget(target, 1, time.Millisecond, 20*time.Millisecond)
		if err == nil || wrongSource.S != 0 || wrongSource.Error == "" {
			t.Fatalf("cross-VRF source accepted: %+v %v", wrongSource, err)
		}
		target.SourceAddress = ""
	}
	// The device exists but is not a VRF: it cannot be interpreted as an exit interface.
	target.VRF = "lo"
	invalid, err := icmpRoundTarget(target, 1, time.Millisecond, 20*time.Millisecond)
	if err == nil || invalid.S != 0 || invalid.Error == "" {
		t.Fatalf("ordinary interface accepted as VRF: %+v %v", invalid, err)
	}
}
