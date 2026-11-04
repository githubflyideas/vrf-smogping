package main

import (
	"context"
	"sync"
	"testing"
	"time"
)

type routingSwitchContext struct {
	context.Context
	once        sync.Once
	switchRoute func()
}

func (c *routingSwitchContext) Done() <-chan struct{} { c.once.Do(c.switchRoute); return nil }

func TestRangeReadPinsRoutingContext(t *testing.T) {
	a, b := routingTestTargets(t)
	s, err := NewStore(t.TempDir(), []TargetCfg{a})
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Now().Add(-48 * time.Hour).Unix()
	if err := s.Append(a.Name, Round{T: ts, S: 1, R: 1, MS: []float64{3}}); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureTarget(b); err != nil {
		t.Fatal(err)
	}
	if err := s.Append(b.Name, Round{T: ts, S: 1, R: 1, MS: []float64{7}}); err != nil {
		t.Fatal(err)
	}
	if err := s.EnsureTarget(a); err != nil {
		t.Fatal(err)
	}
	ctx := &routingSwitchContext{Context: context.Background(), switchRoute: func() {
		if err := s.EnsureTarget(b); err != nil {
			t.Error(err)
		}
	}}
	got := s.ReadRange(ctx, a.Name, ts-3600, ts+3600)
	if len(got) != 1 || len(got[0].MS) != 1 || got[0].MS[0] != 3 {
		t.Fatalf("read switched into another routing context: %+v", got)
	}
}
