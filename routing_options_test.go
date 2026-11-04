package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRoutingOverlayMalformedPreservesTargets(t *testing.T) {
	dir := t.TempDir()
	targets := []TargetCfg{{Name: "edge", VRF: "blue"}}
	for _, data := range []string{`null`, `{"edge":{"unexpected":true}}`, `{} {}`, `{"removed":{"vrf":"bad;name","source_address":""}}`, `{"removed":{"vrf":"","source_address":"0.0.0.0"}}`, `{"edge":{"vrf":"red"},"removed":{"source_address":"not-an-ip"}}`} {
		if err := os.WriteFile(filepath.Join(dir, routingOptionsFile), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := loadRoutingOptions(dir, targets); err == nil {
			t.Fatalf("accepted %s", data)
		}
		if targets[0].VRF != "blue" {
			t.Fatal("invalid overlay mutated targets")
		}
	}
}

func TestRoutingOverlayAtomicReplacement(t *testing.T) {
	dir := t.TempDir()
	for _, source := range []string{"127.0.0.1", ""} {
		if err := writeRoutingOptions(dir, map[string]routingOption{"edge": {SourceAddress: source}}); err != nil {
			t.Fatal(err)
		}
		got, err := readRoutingOptions(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got["edge"].SourceAddress != source {
			t.Fatalf("round trip: %+v", got)
		}
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name() != routingOptionsFile {
		t.Fatalf("temporary file leaked: %+v", files)
	}
}

func TestRoutingOverlayExplicitDefaults(t *testing.T) {
	dir := t.TempDir()
	ts := []TargetCfg{{Name: "edge", VRF: "blue", SourceAddress: "192.0.2.1"}}
	if err := os.WriteFile(filepath.Join(dir, "routing-options.json"), []byte(`{"edge":{"vrf":"","source_address":""}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := loadRoutingOptions(dir, ts); err != nil {
		t.Fatal(err)
	}
	if ts[0].VRF != "" || ts[0].SourceAddress != "" {
		t.Fatalf("explicit defaults not applied: %+v", ts)
	}
}
