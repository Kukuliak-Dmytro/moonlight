//go:build windows

package ddc

import (
	"fmt"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	user32 = syscall.NewLazyDLL("user32.dll")
	dxva2  = syscall.NewLazyDLL("dxva2.dll")

	procEnumDisplayMonitors      = user32.NewProc("EnumDisplayMonitors")
	procGetMonitorInfoW          = user32.NewProc("GetMonitorInfoW")
	procEnumDisplayDevicesW      = user32.NewProc("EnumDisplayDevicesW")
	procGetNumberOfPhysMonitors  = dxva2.NewProc("GetNumberOfPhysicalMonitorsFromHMONITOR")
	procGetPhysicalMonitors      = dxva2.NewProc("GetPhysicalMonitorsFromHMONITOR")
	procDestroyPhysicalMonitors  = dxva2.NewProc("DestroyPhysicalMonitors")
	procGetVCPFeatureAndVCPReply = dxva2.NewProc("GetVCPFeatureAndVCPFeatureReply")
	procSetVCPFeature            = dxva2.NewProc("SetVCPFeature")
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

type displayDeviceW struct {
	cb           uint32
	DeviceName   [32]uint16
	DeviceString [128]uint16
	StateFlags   uint32
	DeviceID     [128]uint16
	DeviceKey    [128]uint16
}

type physicalMonitor struct {
	hPhysicalMonitor             uintptr
	szPhysicalMonitorDescription [128]uint16
}

var (
	enumMu       sync.Mutex
	enumHandles  []uintptr
	enumCallback = syscall.NewCallback(func(hMonitor, hdcMonitor, lprcMonitor, dwData uintptr) uintptr {
		enumHandles = append(enumHandles, hMonitor)
		return 1
	})
)

type windowsBackend struct {
	opts Options
}

// NewBackend creates the native Windows Dxva2 DDC/CI backend.
func NewBackend(opts Options) Backend {
	return &windowsBackend{opts: opts}
}

func (b *windowsBackend) Name() string {
	return "windows-dxva2"
}

func (b *windowsBackend) ListMonitors() ([]Monitor, error) {
	enumMu.Lock()
	enumHandles = nil
	r1, _, callErr := procEnumDisplayMonitors.Call(0, 0, enumCallback, 0)
	handles := append([]uintptr(nil), enumHandles...)
	enumHandles = nil
	enumMu.Unlock()

	if r1 == 0 {
		return nil, fmt.Errorf("EnumDisplayMonitors failed: %w", callErr)
	}

	var monitors []Monitor
	idx := 0
	for _, hMon := range handles {
		displayName, deviceID := queryMonitorDeviceStrings(hMon)

		var count uint32
		r1, _, _ := procGetNumberOfPhysMonitors.Call(hMon, uintptr(unsafe.Pointer(&count)))
		if r1 == 0 || count == 0 {
			continue
		}

		phys := make([]physicalMonitor, count)
		r1, _, _ = procGetPhysicalMonitors.Call(
			hMon,
			uintptr(count),
			uintptr(unsafe.Pointer(&phys[0])),
		)
		if r1 == 0 {
			continue
		}

		for _, pm := range phys {
			idx++
			desc := syscall.UTF16ToString(pm.szPhysicalMonitorDescription[:])
			name := desc
			if name == "" {
				name = displayName
			}
			device := displayName
			if deviceID != "" {
				if device != "" {
					device += " (" + deviceID + ")"
				} else {
					device = deviceID
				}
			}

			monitors = append(monitors, &winMonitor{
				pm: pm,
				info: MonitorInfo{
					ID:     fmt.Sprintf("%d", idx),
					Name:   name,
					Device: device,
				},
				opts: b.opts,
			})
		}
	}

	return monitors, nil
}

func queryMonitorDeviceStrings(hMon uintptr) (string, string) {
	var mi monitorInfoExW
	mi.cbSize = uint32(unsafe.Sizeof(mi))
	r1, _, _ := procGetMonitorInfoW.Call(hMon, uintptr(unsafe.Pointer(&mi)))
	if r1 == 0 {
		return "", ""
	}
	devName := syscall.UTF16ToString(mi.szDevice[:])

	var dd displayDeviceW
	dd.cb = uint32(unsafe.Sizeof(dd))
	r1, _, _ = procEnumDisplayDevicesW.Call(
		uintptr(unsafe.Pointer(&mi.szDevice[0])),
		0,
		uintptr(unsafe.Pointer(&dd)),
		0,
	)
	if r1 == 0 {
		return devName, ""
	}
	return devName, syscall.UTF16ToString(dd.DeviceID[:])
}

type winMonitor struct {
	pm     physicalMonitor
	info   MonitorInfo
	opts   Options
	closed bool
}

func (m *winMonitor) Info() MonitorInfo {
	return m.info
}

func (m *winMonitor) GetVCP(code byte) (VCPValue, error) {
	var out VCPValue
	err := WithRetry(m.opts.Retries, m.opts.CommandDelay, func() error {
		var vct, cur, maxVal uint32
		r1, _, callErr := procGetVCPFeatureAndVCPReply.Call(
			m.pm.hPhysicalMonitor,
			uintptr(code),
			uintptr(unsafe.Pointer(&vct)),
			uintptr(unsafe.Pointer(&cur)),
			uintptr(unsafe.Pointer(&maxVal)),
		)
		if r1 == 0 {
			return fmt.Errorf("GetVCPFeature(0x%02X) on %s failed: %w", code, m.info.Name, callErr)
		}
		out = VCPValue{Current: cur, Max: maxVal}
		if m.opts.CommandDelay > 0 {
			time.Sleep(m.opts.CommandDelay)
		}
		return nil
	})
	return out, err
}

func (m *winMonitor) SetVCP(code byte, value uint32) error {
	return WithRetry(m.opts.Retries, m.opts.CommandDelay, func() error {
		r1, _, callErr := procSetVCPFeature.Call(
			m.pm.hPhysicalMonitor,
			uintptr(code),
			uintptr(value),
		)
		if r1 == 0 {
			return fmt.Errorf("SetVCPFeature(0x%02X=%d) on %s failed: %w", code, value, m.info.Name, callErr)
		}
		if m.opts.CommandDelay > 0 {
			time.Sleep(m.opts.CommandDelay)
		}
		return nil
	})
}

func (m *winMonitor) Close() error {
	if m.closed {
		return nil
	}
	m.closed = true
	procDestroyPhysicalMonitors.Call(1, uintptr(unsafe.Pointer(&m.pm)))
	return nil
}
