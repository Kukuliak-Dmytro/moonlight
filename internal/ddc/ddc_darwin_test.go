//go:build darwin

package ddc

import "testing"

func TestParseM1ddcList(t *testing.T) {
	out := "[1] M27UP (493B2707-8E68-4887-B4D7-16C59C59335E)\n[2] (null) (11111111-2222-3333-4444-555555555555)\nnoise line\n"
	got := parseM1ddcList(out)
	if len(got) != 2 {
		t.Fatalf("parsed %d monitors, want 2: %+v", len(got), got)
	}
	if got[0] != (MonitorInfo{ID: "1", Name: "M27UP", Device: "493B2707-8E68-4887-B4D7-16C59C59335E"}) {
		t.Fatalf("first = %+v", got[0])
	}
	if got[1].Name != "External Display 2" || got[1].Device != "11111111-2222-3333-4444-555555555555" {
		t.Fatalf("(null) display = %+v", got[1])
	}
}

func TestM1ddcSelectorPrefersUUID(t *testing.T) {
	withUUID := &m1ddcMonitor{info: MonitorInfo{ID: "2", Device: "UUID-X"}}
	if withUUID.selector() != "UUID-X" {
		t.Fatalf("selector = %q, want the UUID", withUUID.selector())
	}
	noUUID := &m1ddcMonitor{info: MonitorInfo{ID: "2"}}
	if noUUID.selector() != "2" {
		t.Fatalf("selector = %q, want the index fallback", noUUID.selector())
	}
}

func TestParseM1ddcValue(t *testing.T) {
	if v, err := parseM1ddcValue(" 41\n"); err != nil || v != 41 {
		t.Fatalf("v=%d err=%v", v, err)
	}
	for _, bad := range []string{"", "Failed to open display 3", "41 100", "-1", "4.5"} {
		if _, err := parseM1ddcValue(bad); err == nil {
			t.Errorf("parseM1ddcValue(%q) should fail instead of guessing", bad)
		}
	}
}

func TestParseDDCCtlValueTakesCurrentNotMax(t *testing.T) {
	cur, max, err := parseDDCCtlValue("I: VCP control #16 (0x10) = current: 41, max: 100")
	if err != nil || cur != 41 || max != 100 {
		t.Fatalf("cur=%d max=%d err=%v, want 41/100", cur, max, err)
	}
	cur, max, err = parseDDCCtlValue("current: 7, max: 50")
	if err != nil || cur != 7 || max != 50 {
		t.Fatalf("cur=%d max=%d err=%v, want 7/50", cur, max, err)
	}
	if _, _, err := parseDDCCtlValue("D: querying VCP control"); err == nil {
		t.Fatal("output without a current value must error")
	}
}
