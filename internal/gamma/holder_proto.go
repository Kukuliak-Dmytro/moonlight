package gamma

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// protocolVersion identifies the holder's request set. A holder from an older build does
// not know PING, which is how a newer daemon recognizes it and replaces it.
const protocolVersion = "2"

// DefaultLease is how long a gamma setting is held without being refreshed (macOS).
// The daemon refreshes it every poll, so a dead daemon never leaves the screen tinted
// for longer than this.
const DefaultLease = 5 * time.Minute

const (
	minLease = time.Second
	maxLease = 24 * time.Hour
)

var leaseNanos atomic.Int64

func init() { leaseNanos.Store(int64(DefaultLease)) }

// SetLease changes how long the next Apply is held without a refresh. Platforms whose
// gamma ramp persists on its own (Windows) ignore it.
func SetLease(d time.Duration) {
	if d < minLease {
		d = minLease
	}
	if d > maxLease {
		d = maxLease
	}
	leaseNanos.Store(int64(d))
}

// Lease returns the current lease duration.
func Lease() time.Duration { return time.Duration(leaseNanos.Load()) }

// request is one command sent to the macOS gamma holder process.
type request struct {
	reset  bool
	ping   bool // report the protocol version
	touch  bool // extend the lease without changing the tint
	ratios Ratios
	ttl    time.Duration
}

// formatTouch encodes a "TOUCH ttlSeconds" request line.
func formatTouch(ttl time.Duration) string {
	return fmt.Sprintf("TOUCH %d", int(ttl.Seconds()))
}

func clampLease(d time.Duration) time.Duration {
	if d < minLease {
		return minLease
	}
	if d > maxLease {
		return maxLease
	}
	return d
}

// formatSet encodes a "SET r g b ttlSeconds" request line.
func formatSet(r Ratios, ttl time.Duration) string {
	return fmt.Sprintf("SET %.5f %.5f %.5f %d", r[0], r[1], r[2], int(ttl.Seconds()))
}

// parseRequest decodes a request line. Ratios are clamped to the safety floor and the
// ttl to a sane range, so a malformed or hostile client cannot black out the screen
// or pin a tint forever.
func parseRequest(line string) (request, error) {
	f := strings.Fields(line)
	if len(f) == 0 {
		return request{}, fmt.Errorf("empty request")
	}
	switch strings.ToUpper(f[0]) {
	case "RESET":
		return request{reset: true}, nil
	case "PING":
		return request{ping: true}, nil
	case "TOUCH":
		if len(f) != 2 {
			return request{}, fmt.Errorf("TOUCH needs ttl, got %d fields", len(f)-1)
		}
		secs, err := strconv.Atoi(f[1])
		if err != nil {
			return request{}, fmt.Errorf("bad ttl %q", f[1])
		}
		return request{touch: true, ttl: clampLease(time.Duration(secs) * time.Second)}, nil
	case "SET":
		if len(f) != 5 {
			return request{}, fmt.Errorf("SET needs r g b ttl, got %d fields", len(f)-1)
		}
		var r Ratios
		for i := 0; i < 3; i++ {
			v, err := strconv.ParseFloat(f[1+i], 64)
			if err != nil {
				return request{}, fmt.Errorf("bad ratio %q", f[1+i])
			}
			r[i] = v
		}
		secs, err := strconv.Atoi(f[4])
		if err != nil {
			return request{}, fmt.Errorf("bad ttl %q", f[4])
		}
		return request{ratios: ClampRatios(r), ttl: clampLease(time.Duration(secs) * time.Second)}, nil
	default:
		return request{}, fmt.Errorf("unknown command %q", f[0])
	}
}
