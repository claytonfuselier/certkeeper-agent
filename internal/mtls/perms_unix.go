//go:build !windows

package mtls

// restrictFileACL is a no-op on non-Windows platforms.
// Unix file permissions (0600) are set via os.WriteFile and are sufficient.
func restrictFileACL(_ string) error {
	return nil
}
