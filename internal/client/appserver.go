package client

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Codex Desktop starts `codex app-server` on the remote machine over SSH and
// keeps it running; the server keeps the CODEX_HOME its session had. When
// Hearth changes this person's agent home (hearth agents isolate, a new
// identity block), app-servers started before still use the old home (in
// the worst case a shared one without their login) until they restart.

// AppServer is a running Codex app-server (or its proxy) of this person.
type AppServer struct {
	PID       int    `json:"pid"`
	CodexHome string `json:"codex_home"`
	Command   string `json:"command"`
	Ancestor  bool   `json:"ancestor,omitempty"` // the calling process runs inside it
}

// procRoot is /proc (a test points it elsewhere).
var procRoot = "/proc"

// StaleAppServers lists this person's Codex app-servers (their sessions carry
// HEARTH_USER on a shared account) whose CODEX_HOME is not the one Hearth
// sets now but the account-wide default or one an earlier identity block
// set. Other people's processes and homes set on purpose are left out.
// Linux only (Codex Desktop's remote app-servers); nil elsewhere.
func StaleAppServers(home, email string) []AppServer {
	if runtime.GOOS != "linux" || WorkspaceOf(home) == "" || email == "" {
		return nil
	}
	want := agentHomeExports(home)["CODEX_HOME"]
	if want == "" {
		return nil
	}
	h, _ := os.UserHomeDir()
	old := map[string]bool{filepath.Clean(filepath.Join(h, ".codex")): true}
	for _, p := range ReplacedAgentHomes(home)["CODEX_HOME"] {
		old[filepath.Clean(p)] = true
	}
	delete(old, filepath.Clean(want))
	if len(old) == 0 {
		return nil
	}
	anc := ancestors()
	uid := uint32(os.Getuid())
	dirs, _ := filepath.Glob(filepath.Join(procRoot, "[0-9]*"))
	var out []AppServer
	for _, d := range dirs {
		pid, err := strconv.Atoi(filepath.Base(d))
		if err != nil || pid == os.Getpid() {
			continue
		}
		if fi, err := os.Stat(d); err != nil {
			continue
		} else if st, ok := fi.Sys().(*syscall.Stat_t); ok && st.Uid != uid {
			continue
		}
		args := procArgs(d)
		if !isCodexAppServer(args) {
			continue
		}
		env := procEnv(d)
		if env["HEARTH_USER"] != email {
			continue
		}
		ch := env["CODEX_HOME"]
		if ch == "" {
			ch = filepath.Join(h, ".codex")
		}
		if !old[filepath.Clean(ch)] {
			continue
		}
		out = append(out, AppServer{PID: pid, CodexHome: ch, Command: truncHealth(strings.Join(args, " "), 160), Ancestor: anc[pid]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PID < out[j].PID })
	return out
}

func procArgs(dir string) []string {
	b, err := os.ReadFile(filepath.Join(dir, "cmdline"))
	if err != nil {
		return nil
	}
	var out []string
	for _, a := range bytes.Split(bytes.TrimRight(b, "\x00"), []byte{0}) {
		out = append(out, string(a))
	}
	return out
}

func procEnv(dir string) map[string]string {
	b, err := os.ReadFile(filepath.Join(dir, "environ"))
	if err != nil {
		return nil
	}
	out := map[string]string{}
	for _, kv := range bytes.Split(b, []byte{0}) {
		if k, v, ok := strings.Cut(string(kv), "="); ok {
			out[k] = v
		}
	}
	return out
}

// isCodexAppServer: `codex [options] app-server ...`, directly or through
// the npm wrapper (node .../codex.js app-server).
func isCodexAppServer(args []string) bool {
	codex, server := false, false
	for i, a := range args {
		if i < 2 && strings.HasPrefix(filepath.Base(a), "codex") {
			codex = true
		}
		if a == "app-server" {
			server = true
		}
	}
	return codex && server
}

// ancestors are the pids above this process (a command run by a Codex
// session runs inside its app-server).
func ancestors() map[int]bool {
	out := map[int]bool{}
	pid := os.Getppid()
	for i := 0; i < 64 && pid > 1; i++ {
		out[pid] = true
		b, err := os.ReadFile(filepath.Join(procRoot, strconv.Itoa(pid), "stat"))
		if err != nil {
			break
		}
		// pid (comm) state ppid ...: comm may hold spaces, so cut after the last ')'.
		s := string(b)
		f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
		if len(f) < 2 {
			break
		}
		pid, _ = strconv.Atoi(f[1])
	}
	return out
}

// StopAppServers sends SIGTERM to the app-servers and waits up to wait
// for them to exit. It returns the ones still running. Codex Desktop starts
// a new one when it reconnects, through a session that gets the new home.
func StopAppServers(list []AppServer, wait time.Duration) ([]AppServer, error) {
	var errs []string
	for _, a := range list {
		if err := syscall.Kill(a.PID, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
			errs = append(errs, fmt.Sprintf("pid %d: %v", a.PID, err))
		}
	}
	deadline := time.Now().Add(wait)
	for {
		var left []AppServer
		for _, a := range list {
			if syscall.Kill(a.PID, 0) == nil {
				left = append(left, a)
			}
		}
		if len(left) == 0 || time.Now().After(deadline) {
			if len(errs) > 0 {
				return left, fmt.Errorf("%s", strings.Join(errs, "; "))
			}
			return left, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
}
