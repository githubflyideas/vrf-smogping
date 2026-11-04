package main

import (
	"os"
	"strings"
	"testing"
)

func TestDistributionUsesVRFPingName(t *testing.T) {
	cases := []struct {
		file string
		want []string
	}{
		{".goreleaser.yaml", []string{"project_name: vrf-ping", "binary: vrf-ping", "./vrf-ping"}},
		{"package.json", []string{`"name": "vrf-ping-browser-tests"`}},
	}
	for _, tc := range cases {
		t.Run(tc.file, func(t *testing.T) {
			b, err := os.ReadFile(tc.file)
			if err != nil {
				t.Fatal(err)
			}
			for _, want := range tc.want {
				if !strings.Contains(string(b), want) {
					t.Errorf("%s must contain %q", tc.file, want)
				}
			}
		})
	}
}
