package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A bad line must not stop the service from starting: the rest of the list
// starts, the bad line is reported, and FinishConfig returns no error.
func TestStartupSkipsBadLines(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "ping.list"), []byte("127.0.0.1 good\n127.0.0.2 good\n127.0.0.3 slowpoke pace=glacial\n"), 0600)
	os.WriteFile(filepath.Join(dir, "tcp.list"), []byte("127.0.0.1:80 web\n\nthis is broken\n"), 0600)
	cfg := defaultConfig()
	cfg.TargetsDir = dir
	if err := FinishConfig(cfg); err != nil {
		t.Fatalf("startup refused over bad lines: %v", err)
	}
	var names []string
	for _, tg := range cfg.Targets {
		names = append(names, tg.Name)
		if tg.dir == "" {
			t.Errorf("target %q was not validated (no history dir)", tg.Name)
		}
	}
	if strings.Join(names, ",") != "good,web" {
		t.Fatalf("started targets = %v, want good,web", names)
	}
	h := cfg.Health.report()
	if h.OK || len(h.SkippedAtStartup) != 3 {
		t.Fatalf("health = %+v, want 3 skipped", h)
	}
	all := strings.Join(h.SkippedAtStartup, "\n")
	for _, want := range []string{"tcp.list line 3", `tcp target "this" must be host:port`, `duplicate target name: "good"`, `invalid pace "glacial"`} {
		if !strings.Contains(all, want) {
			t.Errorf("skipped report lacks %q:\n%s", want, all)
		}
	}
	// The store still opens, so the history stays readable.
	if _, err := NewStore(t.TempDir(), cfg.Targets); err != nil {
		t.Fatal(err)
	}
}

// Even with every line broken the service starts (empty), so the UI can show
// the history and the problem instead of the process exiting.
func TestStartupWithOnlyBadLines(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "tcp.list"), []byte("nope\n"), 0600)
	cfg := defaultConfig()
	cfg.TargetsDir = dir
	if err := FinishConfig(cfg); err != nil {
		t.Fatalf("startup refused: %v", err)
	}
	if len(cfg.Targets) != 0 || cfg.Health.report().OK {
		t.Fatalf("targets %v, health %+v", cfg.Targets, cfg.Health.report())
	}
	// Truly empty lists are still a configuration error worth stopping for.
	empty := defaultConfig()
	empty.TargetsDir = t.TempDir()
	if err := FinishConfig(empty); err == nil {
		t.Fatal("empty target lists accepted")
	}
}

// Reloads stay all-or-nothing, but the refusal is now visible: Health carries
// the error (and since when) until a reload succeeds, which also clears what
// was skipped at startup.
func TestReloadFailureReportedInHealth(t *testing.T) {
	stubProbes(t)
	cfg := defaultConfig()
	cfg.TargetsDir = t.TempDir()
	path := filepath.Join(cfg.TargetsDir, "tcp.list")
	os.WriteFile(path, []byte("127.0.0.1:80 A\nbroken\n"), 0600)
	if err := FinishConfig(cfg); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(t.TempDir(), cfg.TargetList())
	if err != nil {
		t.Fatal(err)
	}
	mgr := map[string]chan struct{}{"A": make(chan struct{})}
	runningSig["A"] = cfg.TargetList()[0]
	stop, done := make(chan struct{}), make(chan struct{})
	go func() { defer close(done); reloadLoop(cfg, s, NewDetector(s), mgr, stop) }()
	t.Cleanup(func() { close(stop); <-done; delete(runningSig, "A"); delete(runningSig, "B") })

	time.Sleep(3200 * time.Millisecond) // let the watcher take its first stamp
	os.WriteFile(path, []byte("127.0.0.1:80 A\n127.0.0.1:81 B\nbroken\n"), 0600)
	waitFor(t, func() bool { return cfg.Health.report().ReloadError != "" })
	h := cfg.Health.report()
	if h.OK || !strings.Contains(h.ReloadError, "line 3") || h.ReloadErrorSince == 0 || len(h.SkippedAtStartup) != 1 {
		t.Fatalf("health during a refused reload: %+v", h)
	}
	if len(cfg.TargetList()) != 1 {
		t.Fatalf("a refused reload changed the running set: %v", cfg.TargetList())
	}

	os.WriteFile(path, []byte("127.0.0.1:80 A\n127.0.0.1:81 B\n"), 0600)
	waitFor(t, func() bool { return cfg.Health.report().OK })
	if n := len(cfg.TargetList()); n != 2 {
		t.Fatalf("fixed list not applied: %d targets", n)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatal("condition not met within 8s")
}

func TestHealthEndpoint(t *testing.T) {
	srv, cfg, _ := newTestServer(t, map[string]string{"admin": "s3cret"})
	if res := get(t, srv, "/api/health", nil); res.StatusCode != http.StatusUnauthorized {
		t.Fatalf("anonymous /api/health = %d, want 401", res.StatusCode)
	}
	c := sessionOf(t, login(t, srv, "admin", "s3cret"))
	var h HealthReport
	json.NewDecoder(get(t, srv, "/api/health", c).Body).Decode(&h)
	if !h.OK {
		t.Fatalf("fresh health = %+v", h)
	}
	cfg.Health.reloadFailed(errString("targets/tcp.list line 3: bad"), time.Now())
	h = HealthReport{}
	json.NewDecoder(get(t, srv, "/api/health", c).Body).Decode(&h)
	if h.OK || h.ReloadError != "targets/tcp.list line 3: bad" {
		t.Fatalf("health after a failed reload = %+v", h)
	}
}

type errString string

func (e errString) Error() string { return string(e) }

func sessionOf(t *testing.T, res *http.Response) *http.Cookie {
	t.Helper()
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie {
			return c
		}
	}
	t.Fatalf("no %s cookie in %v", sessionCookie, res.Cookies())
	return nil
}

func TestSessionCookieIsVRFPingsOwn(t *testing.T) {
	srv, _, _ := newTestServer(t, map[string]string{"admin": "s3cret"})
	res := login(t, srv, "admin", "s3cret")
	for _, c := range res.Cookies() {
		if c.Name == "pingping_session" {
			t.Fatal("vrf-ping still issues pingping's cookie name")
		}
	}
	c := sessionOf(t, res)
	if c.Name != "vrfping_session" {
		t.Fatalf("cookie name %q", c.Name)
	}
	// pingping's cookie on the same host is ignored, not mistaken for ours.
	if r := get(t, srv, "/api/targets", &http.Cookie{Name: "pingping_session", Value: c.Value}); r.StatusCode != http.StatusUnauthorized {
		t.Fatalf("pingping_session accepted: %d", r.StatusCode)
	}
}

func TestLoginLockout(t *testing.T) {
	old := loginFailDelay
	loginFailDelay = 0
	t.Cleanup(func() { loginFailDelay = old })
	srv, _, _ := newTestServer(t, map[string]string{"admin": "s3cret"})
	for i := 1; i <= 8; i++ {
		want := http.StatusUnauthorized
		if i > loginFreeFails+1 {
			want = http.StatusTooManyRequests
		}
		if res := login(t, srv, "admin", "wrong"); res.StatusCode != want {
			t.Fatalf("wrong password %d: %d, want %d", i, res.StatusCode, want)
		}
	}
	res := login(t, srv, "admin", "s3cret")
	if res.StatusCode != http.StatusTooManyRequests || res.Header.Get("Retry-After") == "" {
		t.Fatalf("right password while locked: %d Retry-After=%q, want 429", res.StatusCode, res.Header.Get("Retry-After"))
	}
	for _, c := range res.Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			t.Fatal("a locked address was issued a session")
		}
	}
}

func checkSecurityHeaders(t *testing.T, label string, res *http.Response) {
	t.Helper()
	h := res.Header
	for k, want := range map[string]string{
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "same-origin",
	} {
		if got := h.Get(k); got != want {
			t.Errorf("%s: %s = %q, want %q", label, k, got, want)
		}
	}
	if !strings.Contains(h.Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Errorf("%s: CSP %q lacks frame-ancestors 'none'", label, h.Get("Content-Security-Policy"))
	}
}

func TestSecurityHeadersEverywhere(t *testing.T) {
	srv, _, _ := newTestServer(t, map[string]string{"admin": "s3cret"})
	c := sessionOf(t, login(t, srv, "admin", "s3cret"))
	for _, tc := range []struct {
		path   string
		cookie *http.Cookie
		page   string
	}{
		{"/login", nil, "login.html"},
		{"/", nil, ""}, // redirect
		{"/", c, "index.html"},
		{"/api/targets", nil, ""},
		{"/api/targets", c, ""},
		{"/api/version", nil, ""},
		{"/api/health", c, ""},
		{"/static/routing.js", nil, ""},
		{"/nope", nil, ""},
	} {
		res := get(t, srv, tc.path, tc.cookie)
		label := tc.path
		checkSecurityHeaders(t, label, res)
		if tc.page == "" {
			continue
		}
		csp := res.Header.Get("Content-Security-Policy")
		b, _ := staticFS.ReadFile("static/" + tc.page)
		hashes := inlineScriptHashes(b)
		if len(hashes) == 0 {
			t.Fatalf("%s: no inline scripts found — the matcher has stopped working", tc.page)
		}
		for _, hs := range hashes {
			if !strings.Contains(csp, hs) {
				t.Errorf("%s: CSP lacks hash %s", label, hs)
			}
		}
		if !strings.Contains(csp, "default-src 'self'") || !strings.Contains(csp, "object-src 'none'") || strings.Contains(csp, "unsafe-eval") {
			t.Errorf("%s: page CSP wrong: %q", label, csp)
		}
	}
}
