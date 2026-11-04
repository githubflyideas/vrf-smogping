package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func ownedTestTarget(t *testing.T, name, vrf, source string) TargetCfg {
	t.Helper()
	c := &Config{Targets: []TargetCfg{{Name: name, Type: "tcp", Host: "127.0.0.1", Port: 80, VRF: vrf, SourceAddress: source}}}
	if err := validateTargets(c); err != nil {
		t.Fatal(err)
	}
	return c.Targets[0]
}

func TestRoutingOwnershipRejectsArchivedLegacyCollision(t *testing.T) {
	scoped := ownedTestTarget(t, "link", "blue", "")
	legacy := ownedTestTarget(t, "link--route-d3890f8dd02b2405fc6bea50", "", "")
	if scoped.dir != legacy.dir {
		t.Fatalf("fixture collision changed: %s / %s", scoped.dir, legacy.dir)
	}
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "reload", true: "restart"}[restart], func(t *testing.T) {
			root := t.TempDir()
			s, err := NewStore(root, []TargetCfg{legacy})
			if err != nil {
				t.Fatal(err)
			}
			if err = s.Append(legacy.Name, Round{T: time.Now().Unix(), S: 1, R: 1}); err != nil {
				t.Fatal(err)
			}
			path := s.dayFile(legacy.Name, time.Now().Format("2006-01-02"))
			before, _ := os.ReadFile(path)
			s.RemoveTarget(legacy.Name)
			if restart {
				_, err = NewStore(root, []TargetCfg{scoped})
			} else {
				err = s.EnsureTarget(scoped)
			}
			if err == nil {
				t.Error("adopted unrelated archived legacy history")
			}
			after, _ := os.ReadFile(path)
			if string(after) != string(before) {
				t.Error("legacy history changed")
			}
		})
	}
}

func TestRoutingOwnershipRejectsReverseCollision(t *testing.T) {
	scoped := ownedTestTarget(t, "link", "blue", "")
	legacy := ownedTestTarget(t, scoped.dir, "", "")
	for _, restart := range []bool{false, true} {
		t.Run(map[bool]string{false: "reload", true: "restart"}[restart], func(t *testing.T) {
			root := t.TempDir()
			s, err := NewStore(root, []TargetCfg{scoped})
			if err != nil {
				t.Fatal(err)
			}
			s.RemoveTarget(scoped.Name)
			if restart {
				_, err = NewStore(root, []TargetCfg{legacy})
			} else {
				err = s.EnsureTarget(legacy)
			}
			if err == nil {
				t.Fatal("legacy target adopted owned scoped directory")
			}
		})
	}
}

func TestRoutingOwnershipVerifiesEveryPersistedField(t *testing.T) {
	for _, marker := range []string{
		`{"name":"other","vrf":"blue","source_address":"127.0.0.2"}`,
		`{"name":"link","vrf":"red","source_address":"127.0.0.2"}`,
		`{"name":"link","vrf":"blue","source_address":"127.0.0.3"}`,
		`{broken`,
	} {
		t.Run(marker, func(t *testing.T) {
			target := ownedTestTarget(t, "link", "blue", "127.0.0.2")
			root := t.TempDir()
			s, err := NewStore(root, []TargetCfg{target})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, target.dir, routingOwnerFile)
			if err = os.WriteFile(path, []byte(marker), 0644); err != nil {
				t.Fatal(err)
			}
			if err = s.EnsureTarget(target); err == nil {
				t.Error("unchanged active context bypassed ownership validation")
			}
			if _, err = NewStore(root, []TargetCfg{target}); err == nil {
				t.Error("restart bypassed ownership validation")
			}
			if b, err := os.ReadFile(path); err != nil || string(b) != marker {
				t.Fatalf("foreign marker overwritten: %q %v", b, err)
			}
		})
	}
}

func TestRoutingOwnershipReplaysMatchingOwnerAfterRestart(t *testing.T) {
	target := ownedTestTarget(t, "link", "blue", "127.0.0.2")
	root := t.TempDir()
	s, err := NewStore(root, []TargetCfg{target})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Append(target.Name, Round{T: time.Now().Unix(), MS: []float64{42}}); err != nil {
		t.Fatal(err)
	}
	s, err = NewStore(root, []TargetCfg{target})
	if err != nil {
		t.Fatal(err)
	}
	s.Replay()
	if got := s.Recent(target.Name, 0); len(got) != 1 || got[0].MS[0] != 42 {
		t.Fatalf("owned history not replayed: %+v", got)
	}
}

func TestRoutingOwnershipRejectsUnownedGeneratedFiles(t *testing.T) {
	scoped := ownedTestTarget(t, "link", "blue", "")
	root := t.TempDir()
	dir := filepath.Join(root, scoped.dir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "notes.txt")
	if err := os.WriteFile(path, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(root, []TargetCfg{scoped}); err == nil {
		t.Fatal("adopted nonempty unowned generated directory")
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "keep" {
		t.Fatalf("user file changed: %q %v", b, err)
	}
}
