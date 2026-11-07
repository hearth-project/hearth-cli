package main

import "testing"

func TestWantMenuBarApp(t *testing.T) {
	for _, c := range []struct {
		goos      string
		noApp     bool
		env       string
		installed bool
		want      bool
	}{
		{"darwin", false, "", false, true},   // a Mac without the app: setup installs it
		{"darwin", true, "", false, false},   // --no-app
		{"darwin", false, "1", false, false}, // HEARTH_NO_APP=1
		{"darwin", false, "0", false, true},  // only "1" turns it off
		{"darwin", false, "", true, false},   // already there: hearth app install updates it
		{"linux", false, "", false, false},   // the app runs on macOS only
	} {
		if got := wantMenuBarApp(c.goos, c.noApp, c.env, c.installed); got != c.want {
			t.Errorf("%+v: got %v, want %v", c, got, c.want)
		}
	}
}
