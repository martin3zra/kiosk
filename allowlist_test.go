package kiosk

import (
	"reflect"
	"testing"
)

func TestParseAllowlist(t *testing.T) {
	got := ParseAllowlist(" Dev@Example.com , other@example.com,, third@example.com ")
	want := map[string]bool{
		"dev@example.com":   true,
		"other@example.com": true,
		"third@example.com": true,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseAllowlist: got %v, want %v", got, want)
	}

	if got := ParseAllowlist(""); len(got) != 0 {
		t.Fatalf("expected an empty allowlist for an empty string, got %v", got)
	}
}
