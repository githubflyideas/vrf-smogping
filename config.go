package main

import (
	"crypto/sha256"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// vrf-ping: a probe, text files, and a smoke graph. Nothing else.
// Probe defaults remain constants. Targets come from lists, with optional
// per-target routing overrides persisted by the authenticated Web UI.
type Config struct {
	Listen        string
	DataDir       string
	TargetsDir    string
	Targets       []TargetCfg
	Probe         ProbeCfg
	HotDays       int // full samples kept this long
	RetentionDays int // downsampled data kept this long

	// Targets is written by the reload loop while web handlers read it. Once the
	// program is running, go through SetTargets / TargetList only.
	tmu sync.RWMutex
}

func (c *Config) SetTargets(ts []TargetCfg) {
	c.tmu.Lock()
	c.Targets = ts
	c.tmu.Unlock()
}

// TargetList returns a copy that is safe to use after a concurrent reload.
func (c *Config) TargetList() []TargetCfg {
	c.tmu.RLock()
	defer c.tmu.RUnlock()
	return append([]TargetCfg(nil), c.Targets...)
}

func defaultConfig() *Config {
	return &Config{
		Listen:        "0.0.0.0:8517",
		DataDir:       "./data",
		TargetsDir:    "./targets",
		Probe:         ProbeCfg{IntervalSec: 60, Packets: 20, GapMs: 50, TimeoutMs: 1000},
		HotDays:       30,
		RetentionDays: 300,
	}
}

// FinishConfig loads targets from the lists and validates them.
func FinishConfig(cfg *Config) error {
	listTargets, err := loadTargetLists(cfg.TargetsDir)
	if err != nil {
		return err
	}
	cfg.Targets = append(cfg.Targets, listTargets...)
	if err := loadRoutingOptions(cfg.TargetsDir, cfg.Targets); err != nil {
		return err
	}
	if len(cfg.Targets) == 0 {
		return fmt.Errorf("no targets: add one line to %s/ping.list or tcp.list", cfg.TargetsDir)
	}
	return validateTargets(cfg)
}

type TargetCfg struct {
	VRF           string `json:"vrf,omitempty"`
	SourceAddress string `json:"source_address,omitempty"`
	Name          string `json:"name"`
	Type          string `json:"type"` // "icmp" (default) | "tcp"
	Host          string `json:"host"`
	Port          int    `json:"port"`         // tcp only
	Pace          string `json:"pace"`         // "fast"(15s) | "normal"(60s) | "slow"(300s)
	IntervalSec   int    `json:"interval_sec"` // explicit seconds, highest priority
	dir           string
}

type ProbeCfg struct {
	IntervalSec int `json:"interval_sec"`
	Packets     int `json:"packets"`
	GapMs       int `json:"gap_ms"`
	TimeoutMs   int `json:"timeout_ms"`
}

var dirSan = regexp.MustCompile(`[^a-zA-Z0-9._\p{Han}-]`)
var vrfName = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{0,14}$`)

// Empty routing options intentionally leave selection to the operating system.
func validateRouting(t TargetCfg) error {
	if t.VRF != "" && !vrfName.MatchString(t.VRF) {
		return fmt.Errorf("target %q: invalid VRF name", t.Name)
	}
	if t.SourceAddress != "" {
		ip := net.ParseIP(t.SourceAddress)
		if ip == nil || ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.IPv4bcast) {
			return fmt.Errorf("target %q: source address must be a unicast IP literal", t.Name)
		}
		if (t.Type == "icmp" || t.Type == "") && ip.To4() == nil {
			return fmt.Errorf("target %q: ICMP currently requires an IPv4 source address", t.Name)
		}
		if host := net.ParseIP(t.Host); host != nil && (host.To4() == nil) != (ip.To4() == nil) {
			return fmt.Errorf("target %q: source and target address families differ", t.Name)
		}
	}
	return nil
}

func validateTargets(cfg *Config) error {
	seen := map[string]bool{}
	seenNames := map[string]bool{}
	for i := range cfg.Targets {
		t := &cfg.Targets[i]
		if t.Name == "" {
			return fmt.Errorf("target #%d has no name", i+1)
		}
		if seenNames[t.Name] {
			return fmt.Errorf("duplicate target name: %q", t.Name)
		}
		seenNames[t.Name] = true
		if t.Type == "" {
			t.Type = "icmp"
		}
		switch t.Type {
		case "icmp":
		case "tcp":
			if t.Port <= 0 {
				return fmt.Errorf("tcp target %q needs a port", t.Name)
			}
		default:
			return fmt.Errorf("target %q: unknown type %q", t.Name, t.Type)
		}
		switch t.Pace {
		case "", "fast", "normal", "slow":
		default:
			return fmt.Errorf("target %q: invalid pace %q (fast|slow)", t.Name, t.Pace)
		}
		if err := validateRouting(*t); err != nil {
			return err
		}
		t.dir = dirSan.ReplaceAllString(t.Name, "_")
		// Preserve every legacy path when both options are empty. A changed
		// routing context starts a separate history, never relabeling old samples.
		if t.VRF != "" || t.SourceAddress != "" {
			h := sha256.Sum256([]byte(t.VRF + "\x00" + t.SourceAddress))
			t.dir += fmt.Sprintf("--route-%x", h[:12])
		}
		if seen[t.dir] {
			return fmt.Errorf("duplicate target name: %q", t.Name)
		}
		seen[t.dir] = true
	}
	return nil
}

// loadTargetLists reads targets/ping.list and tcp.list.
// line format: host[:port]  [name...]  [pace=fast|slow] [interval=sec]
func loadTargetLists(dir string) ([]TargetCfg, error) {
	var out []TargetCfg
	for _, spec := range []struct{ file, typ string }{{"ping.list", "icmp"}, {"tcp.list", "tcp"}} {
		raw, err := os.ReadFile(filepath.Join(dir, spec.file))
		if err != nil {
			continue
		}
		for ln, line := range strings.Split(string(raw), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			t, err := parseListLine(line, spec.typ)
			if err != nil {
				return nil, fmt.Errorf("%s/%s line %d: %w", dir, spec.file, ln+1, err)
			}
			out = append(out, t)
		}
	}
	return out, nil
}

func parseListLine(line, typ string) (TargetCfg, error) {
	t := TargetCfg{Type: typ}
	fields := strings.Fields(line)
	if len(fields) == 0 { // fuzz 发现:纯空白行会越界 —— 上游会挡,但函数自身必须皮实
		return t, fmt.Errorf("empty line")
	}
	addr := fields[0]
	if typ == "tcp" {
		host, portStr, err := net.SplitHostPort(addr)
		if err != nil {
			return t, fmt.Errorf("tcp target %q must be host:port", addr)
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port <= 0 {
			return t, fmt.Errorf("tcp target %q: bad port", addr)
		}
		t.Host, t.Port = host, port
	} else {
		t.Host = addr
	}
	var nameParts []string
	for _, f := range fields[1:] {
		k, v, isKV := strings.Cut(f, "=")
		if !isKV {
			nameParts = append(nameParts, f)
			continue
		}
		switch k {
		case "vrf":
			t.VRF = v
		case "source", "source-address":
			t.SourceAddress = v
		case "pace":
			t.Pace = v
		case "interval":
			n, err := strconv.Atoi(v)
			if err != nil || n <= 0 {
				return t, fmt.Errorf("interval=%q invalid", v)
			}
			t.IntervalSec = n
		} // unknown k=v silently ignored: forward compatibility
	}
	if len(nameParts) > 0 {
		t.Name = strings.Join(nameParts, " ")
	} else {
		t.Name = addr
	}
	return t, nil
}
