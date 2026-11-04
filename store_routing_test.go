package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func routingTestTargets(t *testing.T) (TargetCfg, TargetCfg) {
	t.Helper()
	a := TargetCfg{Name: "link", Host: "127.0.0.1", Type: "tcp", Port: 8080}
	b := a
	b.SourceAddress = "127.0.0.2"
	ca, cb := &Config{Targets: []TargetCfg{a}}, &Config{Targets: []TargetCfg{b}}
	if err := validateTargets(ca); err != nil {
		t.Fatal(err)
	}
	if err := validateTargets(cb); err != nil {
		t.Fatal(err)
	}
	return ca.Targets[0], cb.Targets[0]
}

func TestStoreRoutingContextChangeSeparatesHistory(t *testing.T) {
	a, b := routingTestTargets(t)
	s, err := NewStore(t.TempDir(), []TargetCfg{a})
	if err != nil {
		t.Fatal(err)
	}
	r := Round{T: time.Now().Unix(), S: 1, R: 1, MS: []float64{3}}
	if err := s.Append(a.Name, r); err != nil {
		t.Fatal(err)
	}
	oldFile := s.dayFile(a.Name, time.Now().Format("2006-01-02"))
	if err := s.EnsureTarget(b); err != nil {
		t.Fatal(err)
	}
	if got := s.Recent(b.Name, 0); len(got) != 0 {
		t.Fatalf("old context leaked into new ring: %+v", got)
	}
	if got := s.dayFile(b.Name, time.Now().Format("2006-01-02")); got == oldFile {
		t.Fatal("routing change still writes old directory")
	}
	r.MS = []float64{7}
	if err := s.Append(b.Name, r); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureTarget(a); err != nil {
		t.Fatal(err)
	}
	got := s.Recent(a.Name, 0)
	if len(got) != 1 || got[0].MS[0] != 3 {
		t.Fatalf("return to default lost or mixed history: %+v", got)
	}
	if _, err := os.Stat(filepath.Join(s.dir, b.dir, time.Now().Format("2006-01-02")+".jsonl")); err != nil {
		t.Fatal(err)
	}
}
