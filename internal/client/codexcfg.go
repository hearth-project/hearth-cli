package client

import (
	"bufio"
	"bytes"
	"context"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/pelletier/go-toml/v2"
)

// Which Codex config a session of this person really uses, and whether it
// goes through their codex-mixin gateway. On a shared account another
// person's rc lines can export CODEX_HOME: Codex then uses their config
// (e.g. a ChatGPT login that cannot reach the internet from here), and
// every request hangs although everything looks installed.

// CodexRoute is the effective Codex configuration of a session.
type CodexRoute struct {
	Home     string `json:"home"`               // effective CODEX_HOME
	SetBy    string `json:"set_by,omitempty"`   // "file:line" of the assignment, when not this person's own
	Provider string `json:"provider,omitempty"` // model_provider
	BaseURL  string `json:"base_url,omitempty"`
	Gateway  bool   `json:"gateway"` // a local gateway (codex-mixin)
	Reach    *bool  `json:"reach,omitempty"`
	Detail   string `json:"detail,omitempty"` // why it cannot reach it
	// Shared: on a shared account (a workspace) it is the account-wide
	// default ~/.codex, which everybody's Codex falls back to.
	Shared bool `json:"shared,omitempty"`
	// Missing: login files absent from Home (auth.json, config.toml);
	// Codex and Codex Desktop then ask to log in.
	Missing []string `json:"missing,omitempty"`
}

func loginShell() string {
	if sh := os.Getenv("SHELL"); sh != "" {
		return sh
	}
	if runtime.GOOS == "darwin" {
		return "/bin/zsh"
	}
	return "/bin/sh"
}

// runShell runs script with a timeout, killing the whole process group.
func runShell(name string, args []string, env []string, timeout time.Duration) ([]byte, []byte) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = time.Second
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	_ = cmd.Run()
	return out.Bytes(), errb.Bytes()
}

// sessionEnv are the variables a session of this person carries before
// its rc files run: HEARTH_USER on a shared account (their own sshd sets
// it), so the check sees what their Codex sessions see.
func sessionEnv(home, email string) []string {
	env := []string{"CODEX_HOME="} // as a fresh SSH session: not inherited from the daemon
	if WorkspaceOf(home) != "" && email != "" {
		env = append(env, "HEARTH_USER="+email)
	}
	return env
}

// loginCodexHome is CODEX_HOME in a login shell of this person ("" = unset).
func loginCodexHome(home, email string) string {
	const mark = "__HEARTH_CODEX_HOME__="
	cmd := `printf '\n%s%s\n' "` + mark + `" "${CODEX_HOME:-}"`
	if filepath.Base(loginShell()) == "bash" {
		// SSH sessions with a command (an IDE's server) read ~/.bashrc even
		// when no login file sources it.
		cmd = `[ -r ~/.bashrc ] && . ~/.bashrc >/dev/null 2>&1; ` + cmd
	}
	out, _ := runShell(loginShell(), []string{"-lic", cmd}, sessionEnv(home, email), 8*time.Second)
	for _, l := range strings.Split(string(out), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(l), mark); ok {
			return v
		}
	}
	return ""
}

var traceRE = regexp.MustCompile(`^\++([^:|]*):(\d+)\|([^:|]*):(\d*) (.*)$`)

// codexHomeSetBy traces a bash login shell to find the last assignment of
// CODEX_HOME and the file that sourced it ("a:15 (sourced from b:483)").
func codexHomeSetBy(home, email string) string {
	if filepath.Base(loginShell()) != "bash" {
		return ""
	}
	// PS4 is not taken from the environment by a root shell: set it inside,
	// then read the login files the way a login shell does. Each trace line
	// names its file and line and the line that sourced that file.
	script := `PS4='+${BASH_SOURCE}:${LINENO}|${BASH_SOURCE[1]}:${BASH_LINENO[0]} '; set -x; ` +
		`[ -r /etc/profile ] && . /etc/profile; ` +
		`for f in ~/.bash_profile ~/.bash_login ~/.profile; do [ -r "$f" ] && { . "$f"; break; }; done; ` +
		`[ -r ~/.bashrc ] && . ~/.bashrc; set +x`
	_, errb := runShell("bash", []string{"--noprofile", "--norc", "-i", "-c", script}, sessionEnv(home, email), 10*time.Second)
	last := ""
	sc := bufio.NewScanner(bytes.NewReader(errb))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		m := traceRE.FindStringSubmatch(sc.Text())
		if m == nil || m[1] == "" {
			continue
		}
		cmd := m[5]
		if !strings.Contains(cmd, "CODEX_HOME=") || strings.HasPrefix(cmd, "printf") {
			continue
		}
		last = m[1] + ":" + m[2]
		if m[3] != "" && m[3] != m[1] {
			last += " (sourced from " + m[3] + ":" + m[4] + ")"
		}
	}
	return last
}

// codexLoginMissing lists the login files a Codex home lacks.
func codexLoginMissing(dir string) []string {
	var out []string
	for _, f := range []string{"auth.json", "config.toml"} {
		if _, err := os.Stat(filepath.Join(dir, f)); err != nil {
			out = append(out, f)
		}
	}
	return out
}

// codexConfigRoute reads model_provider and its base_url from a config.toml.
func codexConfigRoute(dir string) (provider, base string) {
	b, err := os.ReadFile(filepath.Join(dir, "config.toml"))
	if err != nil {
		return "", ""
	}
	var c struct {
		ModelProvider  string `toml:"model_provider"`
		OpenAIBaseURL  string `toml:"openai_base_url"`
		ModelProviders map[string]struct {
			BaseURL string `toml:"base_url"`
		} `toml:"model_providers"`
	}
	if toml.Unmarshal(b, &c) != nil {
		return "", ""
	}
	provider = c.ModelProvider
	if provider == "" {
		provider = "openai"
	}
	base = c.ModelProviders[provider].BaseURL
	if base == "" && provider == "openai" {
		base = c.OpenAIBaseURL
		if base == "" {
			base = "https://api.openai.com/v1"
		}
	}
	return provider, base
}

func isLoopbackURL(u string) bool {
	p, err := url.Parse(u)
	if err != nil {
		return false
	}
	h := p.Hostname()
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// reachable asks base/models once (no model call, any HTTP answer counts).
func reachable(base string) (bool, string) {
	if base == "" {
		return false, "no base_url"
	}
	c := &http.Client{Timeout: 10 * time.Second, Transport: fastTransport()}
	resp, err := c.Get(strings.TrimRight(base, "/") + "/models")
	if err != nil {
		return false, truncHealth(err.Error(), 160)
	}
	resp.Body.Close()
	if resp.StatusCode >= 500 {
		return false, resp.Status
	}
	return true, ""
}

var codexRouteCache struct {
	sync.Mutex
	at      time.Time
	running bool
	r       *CodexRoute
}

// CodexRouteNow works the route out now (login shell, config, one request).
func CodexRouteNow(home, email string) CodexRoute {
	h, _ := os.UserHomeDir()
	r := CodexRoute{Home: loginCodexHome(home, email)}
	if r.Home == "" {
		r.Home = filepath.Join(h, ".codex")
	}
	r.Provider, r.BaseURL = codexConfigRoute(r.Home)
	r.Missing = codexLoginMissing(r.Home)
	r.Gateway = isLoopbackURL(r.BaseURL)
	r.Shared = WorkspaceOf(home) != "" && filepath.Clean(r.Home) == filepath.Clean(filepath.Join(h, ".codex"))
	if mine := agentHomeExports(home)["CODEX_HOME"]; mine != "" && filepath.Clean(mine) != filepath.Clean(r.Home) {
		r.SetBy = codexHomeSetBy(home, email)
	}
	ok, detail := reachable(r.BaseURL)
	r.Reach, r.Detail = &ok, detail
	return r
}

// cachedCodexRoute refreshes the route in the background every 15 minutes
// (FreshChecks: now); nil until the first one finished.
func cachedCodexRoute(home, email string) *CodexRoute {
	run := func() {
		r := CodexRouteNow(home, email)
		codexRouteCache.Lock()
		codexRouteCache.r, codexRouteCache.at, codexRouteCache.running = &r, time.Now(), false
		codexRouteCache.Unlock()
	}
	codexRouteCache.Lock()
	if FreshChecks {
		codexRouteCache.running = true
		codexRouteCache.Unlock()
		run()
		codexRouteCache.Lock()
	} else if !codexRouteCache.running && (codexRouteCache.at.IsZero() || time.Since(codexRouteCache.at) >= 15*time.Minute) {
		codexRouteCache.running = true
		go run()
	}
	defer codexRouteCache.Unlock()
	return codexRouteCache.r
}
