//go:build darwin

package gamma

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Apply sets the color-temperature gamma transfer formula across ALL active macOS displays
// via AppKit + CoreGraphics (JXA), requiring no CGO or external dependencies.
func Apply(ratios Ratios) error {
	r := ClampRatios(ratios)
	script := fmt.Sprintf(`
ObjC.import('AppKit');
ObjC.import('CoreGraphics');
var screens = $.NSScreen.screens;
var count = screens.count;
if (count === 0) {
  $.CGSetDisplayTransferByFormula($.CGMainDisplayID(), 0, %.4f, 1.0, 0, %.4f, 1.0, 0, %.4f, 1.0);
} else {
  for (var i = 0; i < count; i++) {
    var screen = screens.objectAtIndex(i);
    var displayID = screen.deviceDescription.objectForKey($('NSScreenNumber')).unsignedIntValue;
    $.CGSetDisplayTransferByFormula(displayID, 0, %.4f, 1.0, 0, %.4f, 1.0, 0, %.4f, 1.0);
  }
}
`, r[0], r[1], r[2], r[0], r[1], r[2])

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("macOS gamma apply failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Reset restores macOS CoreGraphics color sync gamma tables to default on all displays.
func Reset() error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	script := `ObjC.import('CoreGraphics'); $.CGDisplayRestoreColorSyncSettings();`
	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("macOS gamma reset failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
