package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFinishConfigLoadsOptionalRoutingOverlay(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ping.list"), []byte("127.0.0.1 loopback vrf=blue source=127.0.0.2\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "routing-options.json"), []byte(`{"loopback":{"vrf":"","source_address":""}}`), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := defaultConfig()
	cfg.TargetsDir = dir
	if err := FinishConfig(cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Targets[0].VRF != "" || cfg.Targets[0].SourceAddress != "" {
		t.Fatalf("saved empty overrides not applied: %+v", cfg.Targets[0])
	}
}

func TestSameNameDifferentRoutingRejected(t *testing.T) {
	cfg := &Config{Targets: []TargetCfg{{Name: "same", Host: "127.0.0.1"}, {Name: "same", Host: "127.0.0.1", VRF: "blue"}}}
	if err := validateTargets(cfg); err == nil {
		t.Fatal("names must remain unique across routing contexts")
	}
}
