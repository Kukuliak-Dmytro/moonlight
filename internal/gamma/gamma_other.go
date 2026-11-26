//go:build !windows && !darwin

package gamma

import "errors"

func Apply(ratios Ratios) error {
	return errors.New("gamma night-light is currently implemented for Windows and macOS")
}

func Reset() error {
	return nil
}

// Keepalive has nothing to keep alive on this platform.
func Keepalive() error { return nil }

// EnsureCurrent has no holder to replace on this platform.
func EnsureCurrent() error { return nil }
