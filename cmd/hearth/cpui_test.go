package main

import (
	"testing"
	"time"
)

func TestCPUIWords(t *testing.T) {
	t.Setenv("HOME", "/home/me")
	for in, want := range map[string]string{
		"/home/me/Downloads/x.py": "~/Downloads/x.py",
		"/home/me":                "~",
		"gpu-a:/home/me/x":        "gpu-a:/home/me/x", // another machine's home
		"/data/x":                 "/data/x",
	} {
		if got := shortPath(in); got != want {
			t.Errorf("shortPath(%q) = %q, want %q", in, got, want)
		}
	}
	for d, want := range map[time.Duration]string{
		213 * time.Microsecond:    "210µs",
		8470 * time.Microsecond:   "8.5ms",
		153400 * time.Microsecond: "153ms",
	} {
		if got := roundRTT(d); got != want {
			t.Errorf("roundRTT(%v) = %q, want %q", d, got, want)
		}
	}
}
