//go:build darwin

package gamma

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Apply sets the color-temperature gamma transfer formula on macOS via CoreGraphics (JXA),
// requiring no CGO or external dependencies.
func Apply(ratios Ratios) error {
	r := ClampRatios(ratios)
	script := fmt.Sprintf(`
ObjC.import('CoreGraphics');
var maxDisplays = 8;
var displays = Ref();
var count = Ref();
$.CGSetDisplayTransferByFormula($.CGMainDisplayID(), 0, %.4f, 1.0, 0, %.4f, 1.0, 0, %.4f, 1.0);
`, r[0], r[1], r[2])

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "/usr/bin/osascript", "-l", "JavaScript", "-e", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("macOS gamma apply failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Reset restores macOS CoreGraphics color sync gamma tables to default.
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
