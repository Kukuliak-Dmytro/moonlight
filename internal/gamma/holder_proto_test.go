package gamma

import (
	"strings"
	"testing"
	"time"
)

func TestParseRequestRoundTrip(t *testing.T) {
	line := formatSet(Ratios{1, 0.75, 0.54}, 5*time.Minute)
	req, err := parseRequest(line)
	if err != nil {
		t.Fatal(err)
	}
	if req.reset || req.ratios != (Ratios{1, 0.75, 0.54}) || req.ttl != 5*time.Minute {
		t.Fatalf("round trip gave %+v from %q", req, line)
	}
}

func TestParseRequestClampsHostileInput(t *testing.T) {
	req, err := parseRequest("SET 0 -5 99 999999999")
	if err != nil {
		t.Fatal(err)
	}
	if req.ratios != (Ratios{0.25, 0.25, 1}) {
		t.Fatalf("ratios = %v, want clamped to [0.25 0.25 1]", req.ratios)
	}
	if req.ttl != maxLease {
		t.Fatalf("ttl = %v, want capped at %v", req.ttl, maxLease)
	}
	req, _ = parseRequest("SET 1 1 1 0")
	if req.ttl != minLease {
		t.Fatalf("ttl = %v, want floor %v", req.ttl, minLease)
	}
}

func TestParseRequestRejectsGarbage(t *testing.T) {
	for _, bad := range []string{"", "SET", "SET 1 1 1", "SET a b c 5", "SET 1 1 1 x", "BOOM"} {
		if _, err := parseRequest(bad); err == nil {
			t.Errorf("parseRequest(%q) should fail", bad)
		}
	}
	if r, err := parseRequest("reset"); err != nil || !r.reset {
		t.Fatalf("reset = %+v err=%v", r, err)
	}
}

func TestSetLeaseIsClamped(t *testing.T) {
	defer SetLease(DefaultLease)
	SetLease(0)
	if Lease() != minLease {
		t.Fatalf("lease = %v, want %v", Lease(), minLease)
	}
	SetLease(1000 * time.Hour)
	if Lease() != maxLease {
		t.Fatalf("lease = %v, want %v", Lease(), maxLease)
	}
	if !strings.HasPrefix(formatSet(Neutral(), time.Minute), "SET 1.00000") {
		t.Fatal("unexpected SET format")
	}
}

func TestParseTouch(t *testing.T) {
	req, err := parseRequest(formatTouch(90 * time.Second))
	if err != nil || !req.touch || req.ttl != 90*time.Second {
		t.Fatalf("touch = %+v err=%v", req, err)
	}
	if req, _ := parseRequest("TOUCH 99999999"); req.ttl != maxLease {
		t.Fatalf("touch ttl = %v, want capped at %v", req.ttl, maxLease)
	}
	for _, bad := range []string{"TOUCH", "TOUCH x", "TOUCH 1 2"} {
		if _, err := parseRequest(bad); err == nil {
			t.Errorf("parseRequest(%q) should fail", bad)
		}
	}
}

func TestParsePing(t *testing.T) {
	req, err := parseRequest("PING")
	if err != nil || !req.ping {
		t.Fatalf("ping = %+v err=%v", req, err)
	}
}
