package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRetentionIncludesPreviousRoutingContextAfterRestart(t *testing.T) {
	a, b := routingTestTargets(t)
	root := t.TempDir()
	s, err := NewStore(root, []TargetCfg{b})
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Now().AddDate(0, 0, -400)
	if err := s.Append(b.Name, Round{T: ts.Unix(), S: 1, R: 1, MS: []float64{9}}); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(root, b.dir, ts.Format("2006-01-02")+".jsonl")
	// Restart with both optional fields cleared, while retaining archived history.
	s, err = NewStore(root, []TargetCfg{a})
	if err != nil {
		t.Fatal(err)
	}
	s.Tier(30, 300)
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Fatalf("previous routing history escaped retention: %v", err)
	}
}
