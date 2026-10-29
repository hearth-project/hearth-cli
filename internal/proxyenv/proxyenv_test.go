package proxyenv

import (
	"slices"
	"testing"
)

func TestEnv(t *testing.T) {
	plain := []string{"PATH=/bin"}
	if got := Env(plain); !slices.Equal(got, plain) {
		t.Fatalf("no proxy changed env: %v", got)
	}
	got := Env([]string{"http_proxy=http://p:1", "no_proxy=.corp,127.0.0.1"})
	if !slices.Contains(got, "no_proxy=.corp,127.0.0.1,localhost,::1") || !slices.Contains(got, "NO_PROXY=localhost,127.0.0.1,::1") {
		t.Fatalf("%v", got)
	}
	if again := Env(got); !slices.Equal(again, got) {
		t.Fatalf("not idempotent: %v", again)
	}
}
