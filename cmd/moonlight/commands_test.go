package main

import (
	"regexp"
	"strings"
	"testing"
)

// Every command the usage text advertises must be routed. This is the class of bug that
// once shipped a `gamma-holder` helper the binary could not actually start.
func TestEveryAdvertisedCommandIsRouted(t *testing.T) {
	line := regexp.MustCompile(`(?m)^  ([a-z-]+)\b`)
	special := map[string]bool{"version": true, "help": true} // handled in main itself
	_, section, ok := strings.Cut(usageText, "Commands:\n")
	if !ok {
		t.Fatal("usage text has no Commands: section")
	}
	found := 0
	for _, m := range line.FindAllStringSubmatch(section, -1) {
		name := m[1]
		found++
		if special[name] {
			continue
		}
		if _, ok := commands[name]; !ok {
			t.Errorf("usage advertises %q but commands has no handler for it", name)
		}
	}
	if found < 10 {
		t.Fatalf("parsed only %d commands from the usage text; the pattern is stale", found)
	}
}

func TestInternalCommandsStayRoutedButHidden(t *testing.T) {
	for _, name := range []string{"gamma-holder", "setup"} {
		if _, ok := commands[name]; !ok {
			t.Errorf("%q must stay routed", name)
		}
	}
	if strings.Contains(usageText, "gamma-holder") {
		t.Error("gamma-holder is internal and must not be advertised")
	}
}
