//go:build darwin

package cli

import "os/exec"

// keychainSecret returns the generic-password secret for service from the login Keychain
// via /usr/bin/security (`-w` prints the raw password/blob). ok is false when the item is
// absent or the tool errors. macOS may prompt for Keychain access on first use — the app
// must be granted. This is a local read (no net/*); the offline promise is unaffected.
func keychainSecret(service string) ([]byte, bool) {
	out, err := exec.Command("/usr/bin/security", "find-generic-password", "-s", service, "-w").Output()
	if err != nil {
		return nil, false
	}
	for len(out) > 0 && (out[len(out)-1] == '\n' || out[len(out)-1] == '\r') {
		out = out[:len(out)-1]
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}
