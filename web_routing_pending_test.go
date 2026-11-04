package main

import (
	"encoding/json"
	"testing"
)

func TestRoutingEditorReturnsSavedAndActiveSettings(t *testing.T) {
	srv, cfg, _ := newTestServer(t, nil)
	cfg.TargetsDir = t.TempDir()
	if err := writeRoutingOptions(cfg.TargetsDir, map[string]routingOption{"A": {VRF: "blue", SourceAddress: "127.0.0.1"}}); err != nil {
		t.Fatal(err)
	}
	res := get(t, srv, "/api/routing-options?target=A", nil)
	defer res.Body.Close()
	var body struct {
		Target  TargetCfg
		Saved   TargetCfg `json:"saved_target"`
		Pending bool      `json:"pending_reload"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body.Target.VRF != "" || body.Saved.VRF != "blue" || body.Saved.SourceAddress != "127.0.0.1" || !body.Pending {
		t.Fatalf("pending settings not distinguished: %+v", body)
	}
}
