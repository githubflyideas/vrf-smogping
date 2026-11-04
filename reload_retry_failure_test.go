package main

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type reloadFailureLog struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (w *reloadFailureLog) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.Write(p)
}
func (w *reloadFailureLog) text() string { w.mu.Lock(); defer w.mu.Unlock(); return w.b.String() }

func TestReloadRetriesStorageFailureWithoutFileEdit(t *testing.T) {
	stubProbes(t)
	cfg := defaultConfig()
	cfg.TargetsDir = t.TempDir()
	path := filepath.Join(cfg.TargetsDir, "ping.list")
	if err := os.WriteFile(path, []byte("127.0.0.1 A\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg.Targets = []TargetCfg{{Name: "A", Host: "127.0.0.1"}}
	if err := validateTargets(cfg); err != nil {
		t.Fatal(err)
	}
	old := cfg.TargetList()[0]
	s, err := NewStore(t.TempDir(), cfg.TargetList())
	if err != nil {
		t.Fatal(err)
	}
	oldCh := make(chan struct{})
	mgr := map[string]chan struct{}{"A": oldCh}
	runningSig["A"] = old
	fresh := &Config{Targets: []TargetCfg{old}}
	fresh.Targets[0].SourceAddress = "127.0.0.2"
	if err := validateTargets(fresh); err != nil {
		t.Fatal(err)
	}
	blocker := filepath.Join(s.dir, fresh.Targets[0].dir)
	if err := os.WriteFile(blocker, []byte("collision"), 0600); err != nil {
		t.Fatal(err)
	}
	logs := &reloadFailureLog{}
	previous := log.Writer()
	log.SetOutput(logs)
	t.Cleanup(func() { log.SetOutput(previous) })
	stop, done := make(chan struct{}), make(chan struct{})
	go func() { defer close(done); reloadLoop(cfg, s, NewDetector(s), mgr, stop) }()
	t.Cleanup(func() { close(stop); <-done })
	// Let the real three-second watcher establish its initial stamp.
	time.Sleep(3200 * time.Millisecond)
	if err := os.WriteFile(path, []byte("127.0.0.1 A source=127.0.0.2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Two failed polling attempts must preserve the old target and log only once.
	time.Sleep(6200 * time.Millisecond)
	if isClosed(oldCh) || !reflect.DeepEqual(cfg.TargetList(), []TargetCfg{old}) {
		t.Fatal("failed reload replaced healthy target")
	}
	if err := os.Remove(blocker); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(7 * time.Second)
	for time.Now().Before(deadline) && cfg.TargetList()[0].SourceAddress == "" {
		time.Sleep(20 * time.Millisecond)
	}
	if got := cfg.TargetList()[0]; got.SourceAddress != "127.0.0.2" {
		t.Fatalf("unchanged file was not retried: %+v; logs: %s", got, logs.text())
	}
	if !isClosed(oldCh) {
		t.Error("successful retry did not stop old probe")
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(info.ModTime()) {
		t.Error("test changed target file during retry")
	}
	if n := strings.Count(logs.text(), "target reload failed"); n != 1 {
		t.Errorf("want one deduplicated failure log, got %d: %s", n, logs.text())
	}
}
