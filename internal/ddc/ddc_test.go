package ddc

import (
	"errors"
	"testing"
	"time"
)

type stubMonitor struct {
	info   MonitorInfo
	closed bool
}

func (s *stubMonitor) Info() MonitorInfo             { return s.info }
func (s *stubMonitor) GetVCP(byte) (VCPValue, error) { return VCPValue{}, nil }
func (s *stubMonitor) SetVCP(byte, uint32) error     { return nil }
func (s *stubMonitor) Close() error                  { s.closed = true; return nil }

func TestFilterMonitors(t *testing.T) {
	a := &stubMonitor{info: MonitorInfo{ID: "1", Name: "M27UP", Device: "AAA"}}
	b := &stubMonitor{info: MonitorInfo{ID: "2", Name: "DELL U2723QE", Device: "BBB"}}
	all := []Monitor{a, b}

	if got := FilterMonitors(all, "  "); len(got) != 2 {
		t.Fatalf("empty filter kept %d, want 2", len(got))
	}
	got := FilterMonitors(all, "m27up, nomatch")
	if len(got) != 1 || got[0] != Monitor(a) {
		t.Fatalf("filter kept %v, want only M27UP (case-insensitive)", got)
	}
	if !b.closed || a.closed {
		t.Fatal("unmatched monitors must be closed, matched ones left open")
	}
	if got := FilterMonitors([]Monitor{a}, "bbb"); len(got) != 0 {
		t.Fatal("device substring should match against Device only")
	}
}

func TestWithRetry(t *testing.T) {
	calls := 0
	err := WithRetry(2, 0, func() error {
		calls++
		if calls < 3 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("err=%v calls=%d, want success on 3rd attempt", err, calls)
	}

	calls = 0
	want := errors.New("permanent")
	if err := WithRetry(1, time.Nanosecond, func() error { calls++; return want }); err != want || calls != 2 {
		t.Fatalf("err=%v calls=%d, want last error after 2 attempts", err, calls)
	}

	calls = 0
	_ = WithRetry(-5, 0, func() error { calls++; return want })
	if calls != 1 {
		t.Fatalf("negative retries ran %d times, want 1", calls)
	}
}
