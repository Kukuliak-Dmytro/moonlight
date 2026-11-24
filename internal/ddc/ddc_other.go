//go:build !windows && !darwin

package ddc

import "errors"

type unsupportedBackend struct{}

// NewBackend returns a stub backend on unsupported platforms.
func NewBackend(opts Options) Backend {
	return &unsupportedBackend{}
}

func (b *unsupportedBackend) Name() string {
	return "unsupported"
}

func (b *unsupportedBackend) ListMonitors() ([]Monitor, error) {
	return nil, errors.New("DDC/CI backend is currently implemented for Windows and macOS")
}
