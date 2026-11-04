package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Run the real entry point in a child process so flag parsing and fatal errors
// behave exactly as they do in the executable, without starting any probes.
func TestBrandingCLIHelper(t *testing.T) {
	if os.Getenv("VRF_PING_CLI_HELPER") != "1" {
		return
	}
	for i, arg := range os.Args {
		if arg == "--" {
			os.Args = append([]string{"vrf-ping"}, os.Args[i+1:]...)
			main()
			os.Exit(0)
		}
	}
	t.Fatal("missing CLI argument separator")
}

func TestRuntimeBranding(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		want  string
		fails bool
	}{
		{"version", []string{"--version"}, "vrf-ping " + version + "\n", false},
		{"help", []string{"--help"}, "vrf-ping " + version + " —", false},
		{"help-command", []string{"help"}, "./vrf-ping [flags]", false},
		{"bad-auth", []string{"unknown=value"}, "see ./vrf-ping --help", true},
		{"bad-listen", []string{"--listen", "invalid"}, "see ./vrf-ping --help", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			args := append([]string{"-test.run=^TestBrandingCLIHelper$", "--"}, tc.args...)
			cmd := exec.CommandContext(ctx, os.Args[0], args...)
			cmd.Env = append(os.Environ(), "VRF_PING_CLI_HELPER=1")
			cmd.Dir = t.TempDir()
			output, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatalf("CLI timed out: %s", output)
			}
			if (err != nil) != tc.fails {
				t.Fatalf("unexpected exit: %v\n%s", err, output)
			}
			out := string(output)
			if !strings.Contains(out, tc.want) {
				t.Errorf("expected %q in output:\n%s", tc.want, out)
			}
			// The repository has not moved; its real URL is the one legacy
			// name allowed in help, not an obsolete executable identity.
			withoutRepo := strings.ReplaceAll(out, "https://github.com/githubflyideas/vrf-ping", "REPOSITORY")
			if strings.Contains(withoutRepo, "json-ping") || strings.Contains(withoutRepo, "pingping") {
				t.Errorf("obsolete runtime branding:\n%s", out)
			}
			if tc.name == "help" && !strings.Contains(out, "https://github.com/githubflyideas/vrf-ping/releases") {
				t.Error("help must retain the real release repository URL")
			}
		})
	}
}

func TestEmbeddedWebBranding(t *testing.T) {
	mux := newMux(defaultConfig(), nil, nil)
	for _, tc := range []struct {
		path string
		want []string
	}{
		{"/", []string{
			"<title>vrf-ping — link quality oscilloscope</title>",
			`<div class="mark">vrf-ping `,
			"LIKE VRF-PING?",
			"vrf-ping — a single-binary link quality oscilloscope.",
			"vrf-ping — modern SmokePing alternative",
			"vrf-ping — single-binary link quality oscilloscope",
			"https://github.com/githubflyideas/vrf-ping#sponsor",
			"const url = 'https://github.com/githubflyideas/vrf-ping';",
		}},
		{"/login", []string{
			"<title>Sign in — vrf-ping</title>",
			`<div class="mark">vrf-ping ●</div>`,
			"Sign in to vrf-ping",
		}},
	} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			mux.ServeHTTP(w, httptest.NewRequest(http.MethodGet, tc.path, nil))
			if w.Code != http.StatusOK {
				t.Fatalf("page status = %d", w.Code)
			}
			out := w.Body.String()
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("embedded page missing %q", want)
				}
			}
			withoutRepo := strings.ReplaceAll(out, "https://github.com/githubflyideas/vrf-ping", "REPOSITORY")
			if strings.Contains(withoutRepo, "json-ping") || strings.Contains(withoutRepo, "pingping") {
				t.Error("embedded page retains obsolete branding outside repository URLs")
			}
			if strings.Contains(out, "https://github.com/githubflyideas/json-ping") {
				t.Error("embedded page still points to the source repository")
			}
		})
	}
}

func TestModuleBranding(t *testing.T) {
	data, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if fields := strings.Fields(string(data)); len(fields) < 2 || fields[0] != "module" || fields[1] != "vrf-ping" {
		t.Fatalf("module must be vrf-ping, got %s", data)
	}
}

func TestBrandingPreservesProbeAndStorageCompatibility(t *testing.T) {
	cfg := defaultConfig()
	if cfg.DataDir != "./data" || cfg.TargetsDir != "./targets" {
		t.Fatalf("existing storage paths changed: data=%q targets=%q", cfg.DataDir, cfg.TargetsDir)
	}
	if icmpMagic != "pingpingv1!" {
		t.Fatalf("existing probe signature changed: %q", icmpMagic)
	}
}
