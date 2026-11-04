package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const routingOptionsFile = "routing-options.json"

var routingOptionsMu sync.Mutex

type routingOption struct {
	VRF           string `json:"vrf"`
	SourceAddress string `json:"source_address"`
}

func decodeRoutingJSON(r io.Reader, v any) error {
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("expected exactly one JSON object")
	}
	return nil
}

func readRoutingOptions(dir string) (map[string]routingOption, error) {
	b, err := os.ReadFile(filepath.Join(dir, routingOptionsFile))
	if os.IsNotExist(err) {
		return map[string]routingOption{}, nil
	}
	if err != nil {
		return nil, err
	}
	var options map[string]routingOption
	if err := decodeRoutingJSON(strings.NewReader(string(b)), &options); err != nil {
		return nil, fmt.Errorf("routing options: %w", err)
	}
	if options == nil {
		return nil, fmt.Errorf("routing options must be an object")
	}
	// Validate even dormant entries before applying any of the overlay. TCP
	// permits either IP family here; active targets get family/type checks later.
	for name, option := range options {
		if name == "" {
			return nil, fmt.Errorf("routing options: empty target name")
		}
		if err := validateRouting(TargetCfg{Name: name, Type: "tcp", VRF: option.VRF, SourceAddress: option.SourceAddress}); err != nil {
			return nil, fmt.Errorf("routing options: %w", err)
		}
	}
	return options, nil
}

// Explicit empty values override list settings; unknown names are retained for
// temporarily removed targets, but never create new targets.
func loadRoutingOptions(dir string, targets []TargetCfg) error {
	options, err := readRoutingOptions(dir)
	if err != nil {
		return err
	}
	for i := range targets {
		if o, ok := options[targets[i].Name]; ok {
			targets[i].VRF = o.VRF
			targets[i].SourceAddress = o.SourceAddress
		}
	}
	return nil
}

// The sibling temporary file is synced and closed before rename. Never remove
// the destination first: a failed write must preserve the previous bytes.
func writeRoutingOptions(dir string, options map[string]routingOption) error {
	b, err := json.MarshalIndent(options, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".routing-options-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(b, '\n')); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(dir, routingOptionsFile))
}
