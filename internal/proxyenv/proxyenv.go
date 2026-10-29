// Package proxyenv keeps loopback traffic off HTTP proxies. Machines that
// reach the internet through a proxy often set http_proxy / https_proxy
// without no_proxy (or without 127.0.0.1 in it); local services such as
// codex-mixin's gateway then answer "502 Bad Gateway" from the proxy
// instead of themselves. Commands Hearth runs get loopback addresses added
// to no_proxy / NO_PROXY.
package proxyenv

import (
	"os"
	"strings"
)

var loopback = []string{"localhost", "127.0.0.1", "::1"}

// Env returns env (os.Environ() when nil) with loopback hosts in no_proxy
// and NO_PROXY whenever a proxy is set; without a proxy it is unchanged.
func Env(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	proxied := false
	vals := map[string]int{} // no_proxy / NO_PROXY -> index
	for i, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		switch strings.ToLower(k) {
		case "http_proxy", "https_proxy", "all_proxy":
			proxied = proxied || v != ""
		case "no_proxy":
			vals[k] = i
		}
	}
	if !proxied {
		return env
	}
	out := append([]string(nil), env...)
	for _, k := range []string{"no_proxy", "NO_PROXY"} {
		cur := ""
		i, ok := vals[k]
		if ok {
			_, cur, _ = strings.Cut(out[i], "=")
		}
		have := map[string]bool{}
		for _, h := range strings.Split(cur, ",") {
			have[strings.TrimSpace(h)] = true
		}
		next := cur
		for _, h := range loopback {
			if !have[h] {
				if next != "" {
					next += ","
				}
				next += h
			}
		}
		if ok {
			out[i] = k + "=" + next
		} else {
			out = append(out, k+"="+next)
		}
	}
	return out
}
