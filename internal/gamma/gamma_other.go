//go:build !windows && !darwin

package gamma

import "errors"

func Apply(ratios Ratios) error {
	return errors.New("gamma night-light is currently implemented for Windows and macOS")
}

func Reset() error {
	return nil
}
