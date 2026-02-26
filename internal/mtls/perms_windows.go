//go:build windows

package mtls

import (
	"fmt"
	"os/exec"
)

// restrictFileACL sets Windows ACLs on a file to restrict access to SYSTEM and Administrators only.
// This replaces any inherited permissions with an explicit ACL.
func restrictFileACL(path string) error {
	// Disable inheritance and remove inherited ACEs.
	if err := runICACLS(path, "/inheritance:r"); err != nil {
		return fmt.Errorf("removing inheritance: %w", err)
	}

	// Grant SYSTEM full control.
	if err := runICACLS(path, "/grant", "SYSTEM:(F)"); err != nil {
		return fmt.Errorf("granting SYSTEM access: %w", err)
	}

	// Grant Administrators full control.
	if err := runICACLS(path, "/grant", "*S-1-5-32-544:(F)"); err != nil {
		return fmt.Errorf("granting Administrators access: %w", err)
	}

	return nil
}

func runICACLS(path string, args ...string) error {
	cmdArgs := append([]string{path}, args...)
	cmd := exec.Command("icacls", cmdArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("icacls %v: %s: %w", args, string(output), err)
	}
	return nil
}
