//go:build !darwin

package gamma

import "errors"

// RunHolder is only meaningful on macOS, where gamma must be held by a live process.
func RunHolder() error {
	return errors.New("gamma-holder is only used on macOS")
}
