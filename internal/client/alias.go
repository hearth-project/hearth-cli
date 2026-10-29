package client

import (
	"net"
	"regexp"
	"strings"
)

// Machine aliases: the name a machine gets in ~/.ssh/config (and so in
// Codex Desktop's, VS Code's and Cursor's SSH lists), built as
//
//	[family]-[host]-[hardware]-[node]
//
// e.g. gb-johor-edge-gpu-e05-13: lowercase, words joined by "-". family is
// the accelerator family (gb, b, h800, a30, ...; none for a Mac or a CPU
// box), host and node come from the host name, and the hardware word (gpu,
// cpu, arm, mac) is added before the node number when the host name does
// not have it. [ssh.aliases] in config.toml names a machine by hand.

var (
	nonAliasRE = regexp.MustCompile(`[^a-z0-9]+`)
	nodeRE     = regexp.MustCompile(`^(.*?)-([a-z]\d{1,2}-\d+|\d+)$`) // e05-13, 0399
	maskedGPU  = regexp.MustCompile(`^cf-[a-z]+-([a-z]+?)z+\d*(?:-|$)`)
	modelRE    = regexp.MustCompile(`^([a-z]+)(\d+)`)
)

// gpuFamily turns a normalised GPU model into the alias's first word.
func gpuFamily(gpu string) string {
	gpu = strings.ToLower(gpu)
	switch {
	case gpu == "" || gpu == "none" || gpu == "apple":
		return ""
	case maskedGPU.MatchString(gpu): // masked model names: cf-ng-gbzz2-o-l -> gb
		return maskedGPU.FindStringSubmatch(gpu)[1]
	}
	if m := modelRE.FindStringSubmatch(gpu); m != nil {
		switch m[1] {
		case "gb", "gh":
			return m[1] // GB200, GB300: the family is what people say
		case "b":
			return "b"
		}
		return m[1] + m[2] // h800, a30, l40
	}
	return nonAliasRE.ReplaceAllString(gpu, "-")
}

// hardwareWord is the alias's hardware type.
func hardwareWord(facts map[string]any) string {
	s := func(k string) string { v, _ := facts[k].(string); return strings.ToLower(v) }
	switch {
	case s("os") == "darwin":
		return "mac"
	case s("gpu") != "" && s("gpu") != "none" && s("gpu") != "apple":
		return "gpu"
	case s("arch") == "aarch64" || s("arch") == "arm64":
		return "arm"
	}
	return "cpu"
}

// MachineAlias builds a machine's alias from its name and reported facts.
func MachineAlias(name string, facts map[string]any) string {
	host := strings.ToLower(name)
	if net.ParseIP(host) == nil {
		if i := strings.IndexByte(host, '.'); i > 0 {
			host = host[:i]
		}
	}
	host = strings.Trim(nonAliasRE.ReplaceAllString(host, "-"), "-")
	if host == "" {
		host = "machine"
	}
	gpu, _ := facts["gpu"].(string)
	fam := gpuFamily(gpu)
	hw := hardwareWord(facts)
	words := strings.Split(host, "-")
	has := func(w string) bool {
		for _, x := range words {
			if x == w {
				return true
			}
		}
		return false
	}
	if hw == "mac" && strings.Contains(host, "mac") {
		hw = "" // macbook-pro-2 says it already
	} else if has(hw) {
		hw = ""
	}
	if hw != "" {
		if m := nodeRE.FindStringSubmatch(host); m != nil && m[1] != "" && net.ParseIP(name) == nil {
			host = m[1] + "-" + hw + "-" + m[2]
		} else {
			host = hw + "-" + host
		}
	}
	famIn := false
	for _, w := range words {
		rest, ok := strings.CutPrefix(w, fam)
		famIn = famIn || (fam != "" && ok && strings.Trim(rest, "0123456789") == "") // gb, gb200, h800
	}
	if fam != "" && !famIn {
		host = fam + "-" + host
	}
	return host
}

// MachineAliases gives every machine a unique alias: [ssh.aliases] first,
// then MachineAlias; machines that would share one get their id's start.
// Devices with the same name and address (two Hearth homes on one machine)
// are one machine and share the alias.
func MachineAliases(home string, ids, names []string, facts []map[string]any, addrs ...[]string) []string {
	manual := map[string]string{}
	if c, err := UserConfig(home); err == nil && c.SSH != nil {
		for k, v := range c.SSH.Aliases {
			manual[k] = v
		}
	}
	out := make([]string, len(ids))
	count := map[string]int{}
	same := map[string]bool{} // alias|address already counted
	for i := range ids {
		if a := manual[ids[i]]; a != "" {
			out[i] = a
		} else {
			out[i] = MachineAlias(names[i], facts[i])
		}
		addr := ""
		if len(addrs) > 0 && i < len(addrs[0]) {
			addr = addrs[0][i]
		}
		if k := out[i] + "|" + addr; addr == "" || !same[k] {
			same[k] = true
			count[out[i]]++
		}
	}
	for i := range out {
		if count[out[i]] > 1 && manual[ids[i]] == "" {
			out[i] += "-" + ids[i][:min(6, len(ids[i]))]
		}
	}
	return out
}
