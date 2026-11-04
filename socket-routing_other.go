//go:build !linux

package main

import (
	"fmt"
	"runtime"
)

func validateVRF(name string) error {
	if name != "" {
		return fmt.Errorf("VRF binding unsupported on %s", runtime.GOOS)
	}
	return nil
}
func bindVRF(fd uintptr, name string) error { return validateVRF(name) }
