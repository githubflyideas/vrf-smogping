package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestApplyTargetsStorageFailurePreservesRunningSet(t *testing.T) {
	pl := stubProbes(t)
	cfg := defaultConfig()
	cfg.Targets = []TargetCfg{{Name: "A", Host: "127.0.0.1"}, {Name: "B", Host: "127.0.0.2"}}
	if err := validateTargets(cfg); err != nil {
		t.Fatal(err)
	}
	old := cfg.TargetList()
	s, err := NewStore(t.TempDir(), old)
	if err != nil {
		t.Fatal(err)
	}
	mgr := map[string]chan struct{}{}
	for _, tg := range old {
		mgr[tg.Name] = make(chan struct{})
		runningSig[tg.Name] = tg
	}
	chA, chB := mgr["A"], mgr["B"]
	round := Round{T: time.Now().Unix(), S: 1, R: 1, MS: []float64{12}}
	if err := s.AppendTarget(old[0], round); err != nil {
		t.Fatal(err)
	}
	path := s.dayFile("A", time.Now().Format("2006-01-02"))
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	freshCfg := &Config{Targets: cfg.TargetList()}
	for i := range freshCfg.Targets {
		freshCfg.Targets[i].VRF = "blue"
	}
	if err := validateTargets(freshCfg); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(s.dir, freshCfg.Targets[1].dir)
	if err := os.WriteFile(blocker, []byte("not a directory"), 0600); err != nil {
		t.Fatal(err)
	}
	applyTargets(cfg, freshCfg.Targets, s, NewDetector(s), mgr)
	if isClosed(chA) || isClosed(chB) {
		t.Error("storage failure stopped healthy old probe")
	}
	if !reflect.DeepEqual(cfg.TargetList(), old) {
		t.Errorf("storage failure published new routing: %+v", cfg.TargetList())
	}
	if mgr["A"] != chA || mgr["B"] != chB {
		t.Error("storage failure replaced running channels")
	}
	if got := pl.take(); len(got) != 0 {
		t.Errorf("storage failure started replacement probes: %v", got)
	}
	if got := s.Recent("A", 0); !reflect.DeepEqual(got, []Round{round}) {
		t.Errorf("old ring changed: %+v", got)
	}
	if s.dayFile("A", time.Now().Format("2006-01-02")) != path {
		t.Error("old directory changed")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Error("old data bytes changed")
	}
	if err := s.AppendTarget(old[0], round); err != nil {
		t.Fatal(err)
	}
	if got := s.Recent("A", 0); len(got) != 2 {
		t.Error("old probe can no longer append")
	}
}
