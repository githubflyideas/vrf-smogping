package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTierDoesNotAdoptUnownedHashDirectory(t *testing.T) {
	a := ownedTestTarget(t, "link", "", "")
	b := ownedTestTarget(t, "link", "blue", "")
	root := t.TempDir()
	s, err := NewStore(root, []TargetCfg{a})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, b.dir)
	if err = os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"2000-01-01.jsonl", "2000-01-01-notes.txt"} {
		if err = os.WriteFile(filepath.Join(dir, name), []byte("private"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	s.Tier(30, 300)
	for _, name := range []string{"2000-01-01.jsonl", "2000-01-01-notes.txt"} {
		if data, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(data) != "private" {
			t.Errorf("unowned file changed: %s %q %v", name, data, err)
		}
	}
}

func TestRetentionOnlyTouchesRegularExactDayFiles(t *testing.T) {
	for _, method := range []string{"Tier", "Retention"} {
		t.Run(method, func(t *testing.T) {
			a := ownedTestTarget(t, "link", "", "")
			root := t.TempDir()
			s, err := NewStore(root, []TargetCfg{a})
			if err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(root, a.dir)
			names := []string{"2000-01-01.txt", "2000-01-01.jsonl.bak", "2000-02-30.jsonl", "2000-1-01.jsonl"}
			for _, name := range names {
				if err = os.WriteFile(filepath.Join(dir, name), []byte("private"), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if err = os.Mkdir(filepath.Join(dir, "2000-01-02.jsonl"), 0755); err != nil {
				t.Fatal(err)
			}
			if method == "Tier" {
				s.Tier(30, 300)
			} else {
				s.Retention(300)
			}
			for _, name := range names {
				if b, err := os.ReadFile(filepath.Join(dir, name)); err != nil || string(b) != "private" {
					t.Errorf("unrelated file changed: %s %q %v", name, b, err)
				}
			}
			if _, err := os.Stat(filepath.Join(dir, "2000-01-02.jsonl")); err != nil {
				t.Errorf("directory removed: %v", err)
			}
		})
	}
}

func TestTierPreservesSymlinks(t *testing.T) {
	a := ownedTestTarget(t, "link", "", "")
	root := t.TempDir()
	s, err := NewStore(root, []TargetCfg{a})
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(root, "private")
	payload := []byte("{\"t\":1,\"ms\":[1,2,3,4,5,6]}\n")
	if err = os.WriteFile(dest, payload, 0644); err != nil {
		t.Fatal(err)
	}
	for _, age := range []int{400, 40} {
		link := filepath.Join(root, a.dir, time.Now().AddDate(0, 0, -age).Format("2006-01-02")+".jsonl")
		if err = os.Symlink(dest, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	s.Tier(30, 300)
	for _, age := range []int{400, 40} {
		link := filepath.Join(root, a.dir, time.Now().AddDate(0, 0, -age).Format("2006-01-02")+".jsonl")
		if info, err := os.Lstat(link); err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("symlink changed: %s %v", link, err)
		}
	}
	if b, err := os.ReadFile(dest); err != nil || string(b) != string(payload) {
		t.Errorf("symlink destination changed: %q %v", b, err)
	}
}

func TestDownsampleRejectsInvalidDayFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "2000-02-30.jsonl")
	data := []byte("{\"t\":1,\"ms\":[1,2,3,4,5,6]}\n")
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatal(err)
	}
	_ = downsampleFile(path)
	if got, err := os.ReadFile(path); err != nil || string(got) != string(data) {
		t.Fatalf("invalid day file rewritten: %q %v", got, err)
	}
}

func TestDownsamplePreservesExistingTemporaryFile(t *testing.T) {
	a := ownedTestTarget(t, "link", "", "")
	s, err := NewStore(t.TempDir(), []TargetCfg{a})
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Now().AddDate(0, 0, -40)
	if err = s.Append(a.Name, Round{T: ts.Unix(), MS: []float64{1, 2, 3, 4, 5, 6}}); err != nil {
		t.Fatal(err)
	}
	path := s.dayFile(a.Name, ts.Format("2006-01-02")) + ".tmp"
	if err = os.WriteFile(path, []byte("private"), 0644); err != nil {
		t.Fatal(err)
	}
	s.Tier(30, 300)
	if b, err := os.ReadFile(path); err != nil || string(b) != "private" {
		t.Fatalf("existing temp file changed: %q %v", b, err)
	}
}
