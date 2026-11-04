package main

import (
	"os"
	"strings"
	"testing"
)

func TestNativeDistributionOnly(t *testing.T) {
	for _, name := range []string{"Dockerfile", ".github/workflows/docker.yml", "mikrotik"} {
		if _, err := os.Stat(name); !os.IsNotExist(err) {
			t.Errorf("unsupported container distribution remains: %s", name)
		}
	}
	if strings.Contains(strings.ToLower(helpText), "docker") {
		t.Error("CLI still advertises Docker support")
	}
}
