package recipe

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	neturl "net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

var anyVersionRE = regexp.MustCompile(`\d+\.\d+\.\d+[0-9A-Za-z.+-]*`)

func archNode() string {
	if Arch() == "aarch64" {
		return "arm64"
	}
	return "x64"
}

func (s Step) expand(v string) string {
	return strings.NewReplacer("${ARCH}", Arch(), "${ARCH_NODE}", archNode(), "${VERSION}", s.Version).Replace(v)
}

// checkCmd prints the installed version of a tool step.
func (s Step) checkCmd() string {
	switch {
	case s.Check != "":
		return s.Check
	case s.Wrapper != "":
		return shQuote(ExpandHome(s.Wrapper)) + " --version"
	case s.BinDest != "":
		return shQuote(ExpandHome(s.BinDest)) + " --version"
	case s.URL != "" && s.InstallDir != "" && s.Bin == "":
		return "test -d " + shQuote(ExpandHome(s.InstallDir)) + " && echo " + s.Version
	}
	return s.Name + " --version"
}

// toolInstalled reports whether the tool is present at the wanted version.
func (r *runner) toolInstalled(s Step) (bool, string) {
	out, err := r.sh(s.checkCmd(), nil)
	if err != nil {
		return false, ""
	}
	got := anyVersionRE.FindString(string(out))
	want := s.Version
	if i := strings.LastIndex(s.NPM, "@"); want == "" && i > 0 {
		want = s.NPM[i+1:]
	}
	return want == "" || got == strings.TrimPrefix(want, "v"), got
}

func (r *runner) tool(recipe string, s Step, tools map[string]Step) (string, string) {
	if ok, got := r.toolInstalled(s); ok {
		return "ok", got
	}
	if !r.approved(recipe, s) {
		return "needs-approval", s.describe()
	}
	if r.opt.DryRun {
		return "would-run", s.describe()
	}
	var err error
	if s.URL != "" {
		err = r.download(s)
	} else {
		err = r.npm(s, tools)
	}
	if err != nil {
		return "failed", err.Error()
	}
	if ok, got := r.toolInstalled(s); !ok {
		return "failed", fmt.Sprintf("installed, but `%s` reports %q", s.checkCmd(), got)
	}
	return "applied", s.describe()
}

func (s Step) describe() string {
	if s.URL != "" {
		return "download " + s.expand(s.URL) + " (sha256 pinned)"
	}
	d := "npm install -g " + s.NPM
	if s.Node != "" {
		d += " with node from " + s.Node
	}
	if s.Wrapper != "" {
		d += ", wrapper " + s.Wrapper
	}
	return d
}

// fetch downloads url into path and verifies its sha256.
func (r *runner) fetch(url, path, want, proxy string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	hc := &http.Client{Timeout: r.opt.Timeout}
	if proxy != "" {
		pu, err := neturl.Parse(proxy)
		if err != nil {
			return err
		}
		tr := http.DefaultTransport.(*http.Transport).Clone()
		tr.Proxy = http.ProxyURL(pu)
		hc.Transport = tr
	}
	resp, err := hc.Get(url) // honours HTTPS_PROXY / NO_PROXY
	if err != nil {
		return fmt.Errorf("GET %s: %s", url, redact(err.Error()))
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, 2<<30)); err != nil {
		return fmt.Errorf("GET %s: %s", url, redact(err.Error()))
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return fmt.Errorf("sha256 mismatch for %s: got %s, want %s", url, got, want)
	}
	return f.Close()
}

// redact drops query strings (signed redirect URLs carry tokens) from errors.
var queryRE = regexp.MustCompile(`\?[^\s"]*`)

func redact(s string) string { return queryRE.ReplaceAllString(s, "?…") }

func (r *runner) download(s Step) error {
	want := s.SHA256[Arch()]
	if want == "" {
		return fmt.Errorf("no sha256 for architecture %s", Arch())
	}
	url := s.expand(s.URL)
	tmpDir, err := os.MkdirTemp("", "hearth-dl-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	tmp := filepath.Join(tmpDir, filepath.Base(url))
	var errs []string

	routes := []string{""}
	if s.Proxy != "" {
		routes = append(routes, s.Proxy)
	}
	done := false
	for _, proxy := range routes {
		for _, u := range append([]string{url}, s.Mirrors...) {
			u = s.expand(u)
			if err = r.fetch(u, tmp, want, proxy); err == nil {
				done = true
				break
			}
			if proxy != "" {
				err = fmt.Errorf("via proxy %s: %w", proxy, err)
			}
			errs = append(errs, err.Error())
		}
		if done {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	archive := strings.HasSuffix(url, ".tar.gz") || strings.HasSuffix(url, ".tgz") || strings.HasSuffix(url, ".tar.xz")
	if !archive {
		return installExec(tmp, ExpandHome(s.BinDest))
	}
	dir := ExpandHome(s.InstallDir)
	if dir == "" {
		d, err := os.MkdirTemp("", "hearth-x-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(d)
		dir = d
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	args := []string{"-xf", tmp, "-C", dir, "--no-same-owner"}
	if s.Strip > 0 {
		args = append(args, fmt.Sprintf("--strip-components=%d", s.Strip))
	}
	if out, err := exec.Command("tar", args...).CombinedOutput(); err != nil {
		return fmt.Errorf("tar: %v: %s", err, tail(out))
	}
	if s.Bin != "" {
		matches, _ := filepath.Glob(filepath.Join(dir, s.Bin))
		if len(matches) != 1 {
			return fmt.Errorf("bin %q matched %d files in the archive", s.Bin, len(matches))
		}
		dest := ExpandHome(s.BinDest)
		if dest == "" {
			dest = ExpandHome("~/.local/bin/" + s.Name)
		}
		return installExec(matches[0], dest)
	}
	return nil
}

func installExec(src, dest string) error {
	if dest == "" {
		return fmt.Errorf("bin_dest is required for a plain binary")
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return writeAtomic(dest, b, 0o755)
}

func (r *runner) npm(s Step, tools map[string]Step) error {
	npm, env := "npm", os.Environ()
	var nodeHome string
	if s.Node != "" {
		n, ok := tools[s.Node]
		if !ok || n.InstallDir == "" {
			return fmt.Errorf("node tool %q not found (needs a tool step with install_dir)", s.Node)
		}
		nodeHome = ExpandHome(n.InstallDir)
		npm = filepath.Join(nodeHome, "bin", "npm")
		env = append(env, "PATH="+filepath.Join(nodeHome, "bin")+":"+os.Getenv("PATH"))
	}
	args := []string{"install", "-g", s.NPM}
	if s.Registry != "" {
		args = append(args, "--registry="+s.Registry)
	}
	cmd := exec.Command(npm, args...)
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("npm install: %v: %s", err, tail(out))
	}
	if s.Wrapper == "" {
		return nil
	}
	bin := filepath.Join(nodeHome, "bin", s.Name)
	script := fmt.Sprintf("#!/bin/sh\n# Generated by hearth: run %s with the Node in %s\nexport PATH=%s:\"$PATH\"\nexec %s \"$@\"\n",
		s.Name, nodeHome, shQuote(filepath.Join(nodeHome, "bin")), shQuote(bin))
	return writeAtomic(ExpandHome(s.Wrapper), []byte(script), 0o755)
}

// service starts a detached process when its health check fails.
func (r *runner) service(recipe string, s Step) (string, string) {
	if _, err := r.sh(s.Health, nil); err == nil {
		return "ok", "healthy"
	}
	if !r.approved(recipe, s) {
		return "needs-approval", "$ " + s.Start + "  (health: " + s.Health + ")"
	}
	if r.opt.DryRun {
		return "would-run", s.Start
	}
	logDir := filepath.Join(r.opt.StateDir, "services")
	os.MkdirAll(logDir, 0o700)
	logPath := filepath.Join(logDir, s.Name+".log")
	logf, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return "failed", err.Error()
	}
	defer logf.Close()
	cmd := exec.Command("sh", "-c", s.Start)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // survives the terminal/agent
	if err := cmd.Start(); err != nil {
		return "failed", err.Error()
	}
	go cmd.Wait()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		time.Sleep(300 * time.Millisecond)
		if _, err := r.sh(s.Health, nil); err == nil {
			return "applied", "started, log " + logPath
		}
	}
	return "failed", "started but health check still fails; log " + logPath
}
