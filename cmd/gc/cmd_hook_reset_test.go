package main

import (
	"reflect"
	"testing"
)

func TestNormalizeHookProviderFlag(t *testing.T) {
	got := normalizeHookProviderFlag([]string{" pi ", "codex,gemini", ""})
	want := []string{"pi", "codex", "gemini"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("normalizeHookProviderFlag(...) = %q, want %q", got, want)
	}
	if normalizeHookProviderFlag(nil) != nil {
		t.Errorf("nil flag should return nil slice")
	}
	if len(normalizeHookProviderFlag([]string{"", "  ", ","})) != 0 {
		t.Errorf("empty parts should produce empty slice")
	}
}
