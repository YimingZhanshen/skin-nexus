package main

import "testing"

func TestModelDiff(t *testing.T) {
	cases := []struct {
		name     string
		stored   []string
		detected string
		wantOld  string
		wantDiff bool
	}{
		{"agreeing rows need no work", []string{"slim"}, "slim", "slim", false},
		{"NULL row needs work", []string{""}, "slim", "", true},
		{"wrong model needs work", []string{"default"}, "slim", "default", true},
		{"mixed group needs one write for all", []string{"default", ""}, "default", "default/", true},
		{"legacy 64x32 group stays default", []string{"slim"}, "default", "slim", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			old, diff := modelDiff(c.stored, c.detected)
			if old != c.wantOld || diff != c.wantDiff {
				t.Fatalf("modelDiff(%q,%q) = (%q,%v), want (%q,%v)",
					c.stored, c.detected, old, diff, c.wantOld, c.wantDiff)
			}
		})
	}
}
