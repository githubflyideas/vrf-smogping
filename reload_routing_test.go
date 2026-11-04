package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRoutingChangeRestartsProbeAndSwitchesHistory(t *testing.T) {
	pl := stubProbes(t)
	a, b := routingTestTargets(t)
	s, err := NewStore(t.TempDir(), nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	det := NewDetector(s)
	mgr := map[string]chan struct{}{}
	applyTargets(cfg, []TargetCfg{a}, s, det, mgr)
	pl.take()
	old := mgr[a.Name]
	if err := s.Append(a.Name, Round{T: time.Now().Unix(), S: 1, R: 1, MS: []float64{3}}); err != nil {
		t.Fatal(err)
	}
	applyTargets(cfg, []TargetCfg{b}, s, det, mgr)
	if !isClosed(old) {
		t.Fatal("routing change did not stop old probe")
	}
	if len(pl.take()) != 1 {
		t.Fatal("replacement probe not started")
	}
	if len(s.Recent(b.Name, 0)) != 0 {
		t.Fatal("routing change did not isolate history")
	}
	applyTargets(cfg, []TargetCfg{a}, s, det, mgr)
	if len(pl.take()) != 1 || len(s.Recent(a.Name, 0)) != 1 {
		t.Fatal("clearing routing did not restore default-context history")
	}
}

func TestReloadWatchesRoutingOverlay(t *testing.T) {
	stubProbes(t)
	cfg := defaultConfig()
	cfg.TargetsDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(cfg.TargetsDir, "tcp.list"), []byte("127.0.0.1:8080 link\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := FinishConfig(cfg); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(t.TempDir(), cfg.TargetList())
	if err != nil {
		t.Fatal(err)
	}
	det := NewDetector(s)
	mgr := map[string]chan struct{}{}
	applyTargets(cfg, cfg.TargetList(), s, det, mgr)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() { defer close(done); reloadLoop(cfg, s, det, mgr, stop) }()
	defer func() { close(stop); <-done }()
	time.Sleep(100 * time.Millisecond)
	if err := writeRoutingOptions(cfg.TargetsDir, map[string]routingOption{"link": {SourceAddress: "127.0.0.2"}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) {
		if cfg.TargetList()[0].SourceAddress == "127.0.0.2" {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("overlay changes never reached live target configuration")
}
