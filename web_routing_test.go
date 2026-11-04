package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRoutingTargetErrorNotDown(t *testing.T) {
	srv, cfg, store := newTestServer(t, nil)
	targets := cfg.TargetList()
	targets[0].VRF = "blue"
	targets[0].SourceAddress = "127.0.0.1"
	cfg.SetTargets(targets)
	store.Append("A", Round{T: time.Now().Unix(), Error: "source unavailable"})
	res := get(t, srv, "/api/targets", nil)
	var got []struct {
		Down          bool
		Error, VRF    string
		SourceAddress string `json:"source_address"`
	}
	if err := json.NewDecoder(res.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Down || got[0].Error != "source unavailable" || got[0].VRF != "blue" || got[0].SourceAddress != "127.0.0.1" {
		t.Fatalf("target state: %+v", got)
	}
}

func TestRoutingAPIWriteBoundary(t *testing.T) {
	srv, cfg, _ := newTestServer(t, map[string]string{"admin": "secret"})
	cfg.TargetsDir = t.TempDir()
	cookie := login(t, srv, "admin", "secret").Cookies()[0]
	path := filepath.Join(cfg.TargetsDir, routingOptionsFile)
	prior := []byte(`{"A":{"vrf":"","source_address":""}}`)
	os.WriteFile(path, prior, 0600)
	for _, tt := range []struct {
		name, body, origin, content string
		auth                        bool
		code                        int
	}{
		{"unauthorized", `{"name":"A","vrf":"","source_address":""}`, srv.URL, "application/json", false, 401},
		{"foreign origin", `{"name":"A","vrf":"","source_address":""}`, "https://evil.invalid", "application/json", true, 403},
		{"missing origin", `{"name":"A","vrf":"","source_address":""}`, "", "application/json", true, 403},
		{"form", `{}`, srv.URL, "text/plain", true, 415},
		{"unknown field", `{"name":"A","vrf":"","source_address":"","host":"evil"}`, srv.URL, "application/json", true, 400},
		{"trailing", `{"name":"A","vrf":"","source_address":""} {}`, srv.URL, "application/json", true, 400},
		{"unknown target", `{"name":"missing","vrf":"","source_address":""}`, srv.URL, "application/json", true, 404},
		{"invalid source", `{"name":"A","vrf":"","source_address":"not-an-ip"}`, srv.URL, "application/json", true, 400},
		{"oversized", strings.Repeat("x", 9000), srv.URL, "application/json", true, 400},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("PUT", srv.URL+"/api/routing-options", strings.NewReader(tt.body))
			req.Header.Set("Origin", tt.origin)
			req.Header.Set("Content-Type", tt.content)
			if tt.auth {
				req.AddCookie(cookie)
			}
			res, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			res.Body.Close()
			if res.StatusCode != tt.code {
				t.Fatalf("status %d want %d", res.StatusCode, tt.code)
			}
			got, _ := os.ReadFile(path)
			if !bytes.Equal(got, prior) {
				t.Fatal("rejection modified bytes")
			}
		})
	}
	req, _ := http.NewRequest("PUT", srv.URL+"/api/routing-options", strings.NewReader(`{"name":"A","vrf":"","source_address":"127.0.0.1"}`))
	req.AddCookie(cookie)
	req.Header.Set("Origin", srv.URL)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("save: %d", res.StatusCode)
	}
	var got map[string]routingOption
	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got["A"].SourceAddress != "127.0.0.1" {
		t.Fatalf("saved: %s", b)
	}
	if cfg.TargetList()[0].SourceAddress != "" {
		t.Fatal("HTTP handler applied config outside reload loop")
	}
}

func TestRoutingAPIRejectsInvalidPendingOverlay(t *testing.T) {
	srv, cfg, _ := newTestServer(t, map[string]string{"admin": "secret"})
	cfg.TargetsDir = t.TempDir()
	targets := cfg.TargetList()
	targets = append(targets, TargetCfg{Name: "B", Type: "icmp", Host: "127.0.0.1"})
	cfg.SetTargets(targets)
	cookie := login(t, srv, "admin", "secret").Cookies()[0]
	path := filepath.Join(cfg.TargetsDir, routingOptionsFile)
	prior := []byte(`{"A":{"vrf":"","source_address":"::1"}}`)
	if err := os.WriteFile(path, prior, 0600); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("PUT", srv.URL+"/api/routing-options", strings.NewReader(`{"name":"B","vrf":"","source_address":""}`))
	req.AddCookie(cookie)
	req.Header.Set("Origin", srv.URL)
	req.Header.Set("Content-Type", "application/json")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 400 {
		t.Fatalf("invalid pending overlay accepted: %d", res.StatusCode)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, prior) {
		t.Fatal("rejection modified overlay")
	}
}

func TestRoutingAPIReadOnlyWithoutUsers(t *testing.T) {
	srv, _, _ := newTestServer(t, nil)
	res := get(t, srv, "/api/routing-options?target=A", nil)
	var body struct {
		Editable bool
		Target   TargetCfg
	}
	json.NewDecoder(res.Body).Decode(&body)
	if res.StatusCode != 200 || body.Editable || body.Target.Name != "A" {
		t.Fatalf("read only response: %d %+v", res.StatusCode, body)
	}
	req, _ := http.NewRequest("PUT", srv.URL+"/api/routing-options", strings.NewReader(`{}`))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("unauthenticated deployment write: %d", res.StatusCode)
	}
}
