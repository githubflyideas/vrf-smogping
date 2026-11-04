package main

import (
	"os"
	"testing"
	"time"
)

func TestStaleRoutingRoundCannotEnterNewHistory(t *testing.T) {
	a, b := routingTestTargets(t)
	s, err := NewStore(t.TempDir(), []TargetCfg{a})
	if err != nil {
		t.Fatal(err)
	}
	writer, ok := any(s).(interface{ AppendTarget(TargetCfg, Round) error })
	if !ok {
		t.Fatal("store lacks context-aware append")
	}
	oldFile := s.dayFile(a.Name, time.Now().Format("2006-01-02"))
	if err := s.EnsureTarget(b); err != nil {
		t.Fatal(err)
	}
	r := Round{T: time.Now().Unix(), S: 1, R: 1, MS: []float64{9}}
	if err := writer.AppendTarget(a, r); err != nil {
		t.Fatal(err)
	}
	if len(s.Recent(b.Name, 0)) != 0 {
		t.Fatal("stale round contaminated active context")
	}
	if _, err := os.Stat(oldFile); !os.IsNotExist(err) {
		t.Fatalf("stale round unexpectedly written: %v", err)
	}
	if err := writer.AppendTarget(b, r); err != nil {
		t.Fatal(err)
	}
	if len(s.Recent(b.Name, 0)) != 1 {
		t.Fatal("active round not stored")
	}
	s.RemoveTarget(b.Name)
	if err := writer.AppendTarget(b, r); err != nil {
		t.Fatal(err)
	}
	if len(s.Recent(b.Name, 0)) != 0 {
		t.Fatal("removed target resurrected")
	}
}
