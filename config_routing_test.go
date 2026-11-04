package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestOptionalRoutingListFields(t *testing.T) {
	for _, line := range []string{"127.0.0.1 loopback", "127.0.0.1 loopback vrf= source=", "127.0.0.1 loopback vrf=blue source=127.0.0.2"} {
		tg, err := parseListLine(line, "icmp")
		if err != nil {
			t.Fatal(err)
		}
		cfg := &Config{Targets: []TargetCfg{tg}}
		if err := validateTargets(cfg); err != nil {
			t.Fatal(err)
		}
		b, _ := json.Marshal(cfg.Targets[0])
		var got map[string]any
		json.Unmarshal(b, &got)
		if strings.Contains(line, "vrf=blue") {
			if got["vrf"] != "blue" || got["source_address"] != "127.0.0.2" {
				t.Fatalf("routing options lost: %s", b)
			}
		} else if got["vrf"] != nil && got["vrf"] != "" || got["source_address"] != nil && got["source_address"] != "" {
			t.Fatalf("empty options not default: %s", b)
		}
	}
}

func TestRejectInvalidRoutingConfiguration(t *testing.T) {
	for _, options := range []string{
		`"vrf":"../blue"`, `"vrf":"blue;id"`, `"vrf":"0123456789abcdef"`,
		`"source_address":"example.com"`, `"source_address":"192.0.2.1/24"`,
		`"source_address":"0.0.0.0"`, `"source_address":"224.0.0.1"`,
		`"source_address":"::1"`,
	} {
		t.Run(options, func(t *testing.T) {
			var tg TargetCfg
			if err := json.Unmarshal([]byte(`{"name":"a","host":"127.0.0.1","type":"icmp",`+options+`}`), &tg); err != nil {
				t.Fatal(err)
			}
			if err := validateTargets(&Config{Targets: []TargetCfg{tg}}); err == nil {
				t.Fatal("invalid routing configuration accepted")
			}
		})
	}
}

func TestRoutingContextHasSeparateHistoryDirectory(t *testing.T) {
	var dirs []string
	for _, options := range []string{``, `,"vrf":"blue"`, `,"source_address":"127.0.0.2"`, `,"vrf":"blue","source_address":"127.0.0.2"`} {
		var tg TargetCfg
		json.Unmarshal([]byte(`{"name":"link","host":"127.0.0.1"`+options+`}`), &tg)
		cfg := &Config{Targets: []TargetCfg{tg}}
		if err := validateTargets(cfg); err != nil {
			t.Fatal(err)
		}
		dirs = append(dirs, cfg.Targets[0].dir)
	}
	if dirs[0] != "link" {
		t.Fatalf("legacy directory changed: %s", dirs[0])
	}
	seen := map[string]bool{}
	for _, d := range dirs {
		if seen[d] {
			t.Fatalf("routing histories collide: %v", dirs)
		}
		seen[d] = true
	}
}
