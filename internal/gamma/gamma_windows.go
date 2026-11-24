//go:build windows

package gamma

import (
	"fmt"
	"math"
	"sync"
	"syscall"
	"unsafe"
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")
	gdi32  = syscall.NewLazyDLL("gdi32.dll")

	procEnumDisplayMonitors = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW     = user32.NewProc("GetMonitorInfoW")
	procCreateDCW           = gdi32.NewProc("CreateDCW")
	procDeleteDC            = gdi32.NewProc("DeleteDC")
	procSetDeviceGammaRamp  = gdi32.NewProc("SetDeviceGammaRamp")
)

type rect struct {
	Left, Top, Right, Bottom int32
}

type monitorInfoExW struct {
	cbSize    uint32
	rcMonitor rect
	rcWork    rect
	dwFlags   uint32
	szDevice  [32]uint16
}

type gammaRamp struct {
	Red   [256]uint16
	Green [256]uint16
	Blue  [256]uint16
}

var (
	enumMu       sync.Mutex
	enumDevices  []string
	enumCallback = syscall.NewCallback(func(hMonitor, hdcMonitor, lprcMonitor, dwData uintptr) uintptr {
		var mi monitorInfoExW
		mi.cbSize = uint32(unsafe.Sizeof(mi))
		r1, _, _ := procGetMonitorInfoW.Call(hMonitor, uintptr(unsafe.Pointer(&mi)))
		if r1 != 0 {
			dev := syscall.UTF16ToString(mi.szDevice[:])
			if dev != "" {
				enumDevices = append(enumDevices, dev)
			}
		}
		return 1
	})
)

// Apply sets a smooth linear color-temperature gamma ramp across all active Windows displays.
func Apply(ratios Ratios) error {
	r := ClampRatios(ratios)
	ramp := buildRamp(r)

	enumMu.Lock()
	enumDevices = nil
	r1, _, callErr := procEnumDisplayMonitors.Call(0, 0, enumCallback, 0)
	devices := append([]string(nil), enumDevices...)
	enumDevices = nil
	enumMu.Unlock()

	if r1 == 0 {
		return fmt.Errorf("EnumDisplayMonitors failed: %w", callErr)
	}
	if len(devices) == 0 {
		return fmt.Errorf("no display devices found for gamma ramp")
	}

	driverPtr, _ := syscall.UTF16PtrFromString("DISPLAY")
	applied := 0
	var lastErr error

	for _, dev := range devices {
		devPtr, err := syscall.UTF16PtrFromString(dev)
		if err != nil {
			lastErr = err
			continue
		}
		hdc, _, dcErr := procCreateDCW.Call(
			uintptr(unsafe.Pointer(driverPtr)),
			uintptr(unsafe.Pointer(devPtr)),
			0,
			0,
		)
		if hdc == 0 {
			lastErr = fmt.Errorf("CreateDCW(%s) failed: %w", dev, dcErr)
			continue
		}
		ok, _, rampErr := procSetDeviceGammaRamp.Call(hdc, uintptr(unsafe.Pointer(&ramp)))
		procDeleteDC.Call(hdc)
		if ok == 0 {
			lastErr = fmt.Errorf("SetDeviceGammaRamp(%s) failed: %w", dev, rampErr)
			continue
		}
		applied++
	}

	if applied == 0 && lastErr != nil {
		return lastErr
	}
	return nil
}

// Reset restores the neutral identity gamma ramp on all displays.
func Reset() error {
	return Apply(Neutral())
}

func buildRamp(r Ratios) gammaRamp {
	var ramp gammaRamp
	for i := 0; i < 256; i++ {
		base := float64(i) * 257.0
		ramp.Red[i] = clampU16(base * r[0])
		ramp.Green[i] = clampU16(base * r[1])
		ramp.Blue[i] = clampU16(base * r[2])
	}
	return ramp
}

func clampU16(v float64) uint16 {
	rounded := math.Round(v)
	if rounded < 0 {
		return 0
	}
	if rounded > 65535 {
		return 65535
	}
	return uint16(rounded)
}
