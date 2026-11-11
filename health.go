package main

import (
	"sync"
	"time"
)

// configHealth is what the target lists currently get wrong, kept so the web UI
// can show it. Before this, a broken line was visible only in the log: the reload
// loop refused the whole list (keeping the running set, as documented) and every
// later edit, however correct, was refused with it, while the UI looked normal.
type configHealth struct {
	mu          sync.Mutex
	skipped     []string  // lines/targets left out at startup
	reloadErr   string    // why the latest reload was refused, "" if it was not
	reloadSince time.Time // when reloads started failing with reloadErr
}

// HealthReport is the body of GET /api/health.
type HealthReport struct {
	OK               bool     `json:"ok"`
	SkippedAtStartup []string `json:"skipped_at_startup,omitempty"`
	ReloadError      string   `json:"reload_error,omitempty"`
	ReloadErrorSince int64    `json:"reload_error_since,omitempty"` // unix seconds
}

func (h *configHealth) setSkipped(p []string) {
	h.mu.Lock()
	h.skipped = append([]string(nil), p...)
	h.mu.Unlock()
}

// reloadFailed records a refused reload. The start time is kept while the same
// error repeats, so the UI can say how long edits have been ignored.
func (h *configHealth) reloadFailed(err error, now time.Time) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if msg := err.Error(); msg != h.reloadErr {
		if h.reloadErr == "" {
			h.reloadSince = now
		}
		h.reloadErr = msg
	}
}

// reloadOK records a reload that applied the whole list. Every line parsed and
// validated, so whatever was skipped at startup is back too.
func (h *configHealth) reloadOK() {
	h.mu.Lock()
	h.skipped = nil
	h.reloadErr = ""
	h.reloadSince = time.Time{}
	h.mu.Unlock()
}

func (h *configHealth) report() HealthReport {
	h.mu.Lock()
	defer h.mu.Unlock()
	r := HealthReport{
		OK:               len(h.skipped) == 0 && h.reloadErr == "",
		SkippedAtStartup: append([]string(nil), h.skipped...),
		ReloadError:      h.reloadErr,
	}
	if h.reloadErr != "" {
		r.ReloadErrorSince = h.reloadSince.Unix()
	}
	return r
}
