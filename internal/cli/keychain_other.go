//go:build !darwin

package cli

// keychainSecret is a no-op off macOS: there's no login Keychain, so online-quota
// credentials come from the on-disk files only.
func keychainSecret(string) ([]byte, bool) { return nil, false }
