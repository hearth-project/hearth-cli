package client

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Credentials on demand: an on-demand machine (and any other) writes a
// credential only when a skill or command needs it, for a while. The value
// never goes through the agent: `hearth secret use` and the MCP tool
// prepare_credentials write the file and report its path; `hearth secret
// exec` injects env secrets into one command. Files written this way are
// removed when their time is up and are not uploaded from here (they are
// another machine's upload), so removing them deletes nothing on the server.

func tempCredsPath(home string) string { return filepath.Join(home, "temp-credentials.json") }

type tempCred struct {
	Expires int64  `json:"expires"` // unix; 0 = until removed by hand
	SHA256  string `json:"sha256"`
}

func loadTempCreds(home string) map[string]tempCred {
	m := map[string]tempCred{}
	loadJSON(tempCredsPath(home), &m)
	return m
}

func saveTempCreds(home string, m map[string]tempCred) error {
	if len(m) == 0 {
		err := os.Remove(tempCredsPath(home))
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	b, err := jsonIndent(m)
	if err != nil {
		return err
	}
	return writeFileAtomic(tempCredsPath(home), b, 0o600)
}

// tempCredFiles are the files written on demand (absolute paths).
func tempCredFiles(home string) map[string]bool {
	out := map[string]bool{}
	for p := range loadTempCreds(home) {
		out[p] = true
	}
	return out
}

// CredUse reports one credential prepared for use.
type CredUse struct {
	Name   string `json:"name"`
	Path   string `json:"path,omitempty"`
	Action string `json:"action"` // written, present, kept (differs here), env
	For    string `json:"for"`    // the skill or secret asked for
}

func credSum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// UseCredentials writes the credentials of the named skills (their files
// synced encrypted) or file secrets, mode 0600, for ttl (0: until removed
// by hand). Files already there are left alone.
func (e *Engine) UseCredentials(targets []string, ttl time.Duration) ([]CredUse, error) {
	all, err := e.Secrets()
	if err != nil {
		return nil, err
	}
	root := LibraryDir(e.Home)
	temp := loadTempCreds(e.Home)
	var out []CredUse
	matched := map[string]bool{}
	for _, s := range all {
		path, target := "", ""
		if bp, ok := strings.CutPrefix(s.RestoreTo, libRestorePrefix); ok {
			if strings.Contains(bp, "..") {
				continue
			}
			skill := strings.SplitN(strings.TrimPrefix(bp, "skills/"), "/", 2)[0]
			for _, t := range targets {
				if t == skill || t == s.Name {
					target = t
				}
			}
			path = libBundlePath(root, bp)
		} else {
			for _, t := range targets {
				if t == s.Name {
					target = t
				}
			}
			if target != "" && s.RestoreTo == "" {
				matched[target] = true
				out = append(out, CredUse{Name: s.Name, Action: "env", For: target})
				continue
			}
			path = ExpandHome(s.RestoreTo)
		}
		if target == "" {
			continue
		}
		matched[target] = true
		u := CredUse{Name: s.Name, Path: path, For: target}
		if cur, err := os.ReadFile(path); err == nil {
			u.Action = "present"
			if !bytes.Equal(cur, s.Value) {
				u.Action = "kept (differs here)"
			} else if t, ok := temp[path]; ok && ttl > 0 && t.Expires > 0 {
				t.Expires = time.Now().Add(ttl).Unix() // used again: extend
				temp[path] = t
			}
			out = append(out, u)
			continue
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return out, err
		}
		if err := writeFileAtomic(path, s.Value, 0o600); err != nil {
			return out, err
		}
		var exp int64
		if ttl > 0 {
			exp = time.Now().Add(ttl).Unix()
		}
		temp[path] = tempCred{Expires: exp, SHA256: credSum(s.Value)}
		u.Action = "written"
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	if err := saveTempCreds(e.Home, temp); err != nil {
		return out, err
	}
	var missing []string
	for _, t := range targets {
		if !matched[t] {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return out, fmt.Errorf("no credentials stored for %s (hearth secret list; record one with hearth secret set, or keep it inside the skill on a machine that has it)", strings.Join(missing, ", "))
	}
	return out, nil
}

// RemoveTempCredentials removes credential files written on demand: the
// expired ones, or (all) every one. A file changed since it was written is
// the user's now: it stays and is no longer tracked. Returns the paths
// removed.
func RemoveTempCredentials(home string, all bool, only map[string]bool) ([]string, error) {
	temp := loadTempCreds(home)
	now := time.Now().Unix()
	var removed []string
	for p, t := range temp {
		if only != nil && !only[p] {
			continue
		}
		if !all && only == nil && (t.Expires == 0 || t.Expires > now) {
			continue
		}
		b, err := os.ReadFile(p)
		switch {
		case os.IsNotExist(err):
		case err != nil:
			continue
		case credSum(b) != t.SHA256:
			// changed here: the user's file now
		default:
			if err := os.Remove(p); err != nil {
				continue
			}
			removed = append(removed, p)
		}
		delete(temp, p)
	}
	sort.Strings(removed)
	return removed, saveTempCreds(home, temp)
}
