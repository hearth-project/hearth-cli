package client

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/library"
)

// Credential files inside skills (a cloud CLI's credentials, a key, a file
// only its owner may read) do not go into the library bundle, which the
// server keeps in plaintext and teams may share. They travel as end-to-end
// encrypted secrets (restore_to "library:<bundle path>") and are written
// back into the skill on the user's other machines.

const libRestorePrefix = "library:"

// libSecretFiles maps the bundle path of each credential file in the
// library (skills/<name>/...) to the file on disk.
func libSecretFiles(lib library.Library) map[string]string {
	out := map[string]string{}
	add := func(root, prefix string) {
		fs, _ := library.ScanSecrets(root)
		for _, f := range fs {
			out[path.Join(prefix, filepath.ToSlash(f.File))] = filepath.Join(root, f.File)
		}
	}
	// Skills only: the rest of the library (config, recipes) is Hearth's
	// own and refuses credentials (ScanSecrets on push).
	add(lib.SkillsDir(), "skills")
	return out
}

// libBundle bundles the library without its credential files. secSum
// covers those files, so a changed credential is pushed too.
func (e *Engine) libBundle() (data []byte, sum, secSum string, secrets map[string]string, err error) {
	lib := library.Library{Root: LibraryDir(e.Home)}
	secrets = libSecretFiles(lib)
	lib.Exclude = map[string]bool{}
	for bp := range secrets {
		lib.Exclude[bp] = true
	}
	data, sum, err = lib.Bundle()
	if err != nil {
		return nil, "", "", nil, err
	}
	// Written here on demand (another machine's upload, removed later):
	// not this machine's to push, and their removal deletes nothing.
	temp := tempCredFiles(e.Home)
	for bp, p := range secrets {
		if temp[p] {
			delete(secrets, bp)
		}
	}
	return data, sum, libSecretsSum(secrets), secrets, nil
}

func libSecretsSum(files map[string]string) string {
	if len(files) == 0 {
		return ""
	}
	keys := make([]string, 0, len(files))
	for k := range files {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	h := sha256.New()
	for _, k := range keys {
		b, _ := os.ReadFile(files[k])
		c := sha256.Sum256(b)
		h.Write([]byte(k + "\x00" + hex.EncodeToString(c[:]) + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// libSecretName is a secret name for a bundle path: readable, unique.
func libSecretName(bp string) string {
	h := sha256.Sum256([]byte(bp))
	parts := strings.Split(strings.TrimPrefix(bp, "skills/"), "/")
	label := parts[0] + "-" + parts[len(parts)-1]
	label = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, label)
	if len(label) > 100 {
		label = label[:100]
	}
	return "lib-" + label + "-" + hex.EncodeToString(h[:4])
}

// pushLibSecrets uploads changed credential files and deletes the secrets
// of files that are gone. Returns how many were uploaded.
// fileSums maps bundle paths to the sha256 of their files.
func fileSums(files map[string]string) map[string]string {
	out := map[string]string{}
	for bp, p := range files {
		if b, err := os.ReadFile(p); err == nil {
			h := sha256.Sum256(b)
			out[bp] = hex.EncodeToString(h[:])
		}
	}
	return out
}

func (e *Engine) pushLibSecrets(files map[string]string, prev map[string]string) (int, error) {
	had := prev
	all, err := e.Secrets()
	if err != nil {
		return 0, err
	}
	have := map[string]Secret{}
	for _, s := range all {
		if strings.HasPrefix(s.RestoreTo, libRestorePrefix) {
			have[strings.TrimPrefix(s.RestoreTo, libRestorePrefix)] = s
		}
	}
	n := 0
	localOnly := map[string]bool{}
	for bp, p := range files {
		b, err := os.ReadFile(p)
		if err != nil || len(b) == 0 {
			continue
		}
		if s, ok := have[bp]; ok && bytes.Equal(s.Value, b) {
			continue
		}
		s := Secret{Name: libSecretName(bp), Value: b, RestoreTo: libRestorePrefix + bp}
		if !secretFits(s) {
			// Too large to store encrypted, and it may hold a credential, so
			// it does not go into the plaintext bundle either: it stays on
			// this machine and the rest of the library still syncs.
			localOnly[bp] = true
			continue
		}
		if err := e.SetSecret(s); err != nil {
			return n, err
		}
		n++
	}
	for bp, s := range have {
		if _, ok := files[bp]; ok || had[bp] == "" || localOnly[bp] {
			continue // still here, or never here (another machine's upload)
		}
		// Gone from this library since the last sync (deleted, or no
		// longer a credential).
		_ = e.DeleteSecret(s.Name)
	}
	return n, nil
}

// LibLocalOnlyFiles lists the library's credential files that are too
// large to store as a secret (bundle paths). They are neither uploaded
// nor put into the bundle: they stay on this machine.
func LibLocalOnlyFiles(home string) []string {
	return localOnlyLibFiles(libSecretFiles(library.Library{Root: LibraryDir(home)}))
}

func localOnlyLibFiles(files map[string]string) []string {
	var out []string
	for bp, p := range files {
		fi, err := os.Stat(p)
		if err != nil || fi.Size() == 0 {
			continue
		}
		if fi.Size() > api.MaxSecretBytes {
			out = append(out, bp)
			continue
		}
		b, err := os.ReadFile(p)
		if err == nil && !secretFits(Secret{Name: libSecretName(bp), Value: b, RestoreTo: libRestorePrefix + bp}) {
			out = append(out, bp)
		}
	}
	sort.Strings(out)
	return out
}

// keepLocalOnlyLibFiles copies this library's local-only credential files
// into the extracted tree dir (bundle layout) before it replaces the
// library, so a pull does not move them to the backup. Files of skills the
// incoming library no longer has are left out.
func (e *Engine) keepLocalOnlyLibFiles(dir string) error {
	files := libSecretFiles(library.Library{Root: LibraryDir(e.Home)})
	for _, bp := range localOnlyLibFiles(files) {
		rest, ok := strings.CutPrefix(bp, "skills/")
		skill, _, _ := strings.Cut(rest, "/")
		if !ok || skill == "" || strings.Contains(bp, "..") {
			continue
		}
		if _, err := os.Stat(filepath.Join(dir, "skills", skill)); err != nil {
			continue
		}
		dst := filepath.Join(dir, filepath.FromSlash(bp))
		if _, err := os.Lstat(dst); err == nil {
			continue
		}
		if _, err := copyHome(files[bp], dst, "", ""); err != nil {
			return err
		}
	}
	return nil
}

// LibSecretTTL is how long the credential files of a skill that is no
// longer in the library are kept before they are deleted. The wait covers
// another machine that uploaded a new skill's credentials just before its
// library push, which this machine has not pulled yet.
const LibSecretTTL = 7 * 24 * time.Hour

// libSecretNow is the clock of PruneLibSecrets (tests move it).
var libSecretNow = time.Now

// PruneLibSecrets deletes the credential files of skills this library no
// longer has, once they were not updated for LibSecretTTL. Call it right
// after a library sync, so the local library is the account's.
func (e *Engine) PruneLibSecrets() (int, error) {
	lib := library.Library{Root: LibraryDir(e.Home)}
	if _, err := os.Stat(lib.SkillsDir()); err != nil {
		return 0, nil // no library here: nothing to compare with
	}
	all, err := e.Secrets()
	if err != nil {
		return 0, err
	}
	cut := libSecretNow().Add(-LibSecretTTL).Unix()
	n := 0
	for _, s := range all {
		rest, ok := strings.CutPrefix(s.RestoreTo, libRestorePrefix+"skills/")
		if !ok || s.UpdatedAt == 0 || s.UpdatedAt > cut {
			continue
		}
		skill, _, _ := strings.Cut(rest, "/")
		if skill == "" || skill == "." || skill == ".." {
			continue
		}
		if _, err := os.Lstat(filepath.Join(lib.SkillsDir(), skill)); err == nil || !os.IsNotExist(err) {
			continue // still in the library (or cannot tell)
		}
		if e.DeleteSecret(s.Name) == nil {
			n++
		}
	}
	return n, nil
}

// libBundlePath maps a bundle path to the file on this machine.
func libBundlePath(root, bp string) string {
	lib := library.Library{Root: root}
	if rest, ok := strings.CutPrefix(bp, "skills/"); ok {
		return filepath.Join(lib.SkillsDir(), filepath.FromSlash(rest))
	}
	return filepath.Join(root, filepath.FromSlash(bp))
}

// restoreLibSecrets writes the credential files back into the skills this
// library has (mode 0600). Returns how many it wrote.
// On an on-demand machine nothing is written (they come when a skill
// needs them).
func (e *Engine) restoreLibSecrets() (int, error) {
	if OnDemand(e.Home) {
		return 0, nil
	}
	st, _ := loadLibState(e.Home)
	synced := st.SecretFiles
	all, err := e.Secrets()
	if err != nil {
		return 0, err
	}
	root := LibraryDir(e.Home)
	n := 0
	for _, s := range all {
		bp, ok := strings.CutPrefix(s.RestoreTo, libRestorePrefix)
		if !ok || strings.Contains(bp, "..") {
			continue
		}
		p := libBundlePath(root, bp)
		if cur, err := os.ReadFile(p); err == nil {
			if bytes.Equal(cur, s.Value) {
				continue
			}
			// Changed here since the last sync: this machine's copy wins
			// (it is pushed next); unchanged here: take the newer upload.
			h := sha256.Sum256(cur)
			if synced[bp] != hex.EncodeToString(h[:]) {
				continue
			}
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return n, err
		}
		if err := writeFileAtomic(p, s.Value, 0o600); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// LibSecretFiles lists the credential files of the library's skills (bundle
// paths), which sync encrypted instead of in the bundle.
func LibSecretFiles(home string) []string {
	var out []string
	for bp := range libSecretFiles(library.Library{Root: LibraryDir(home)}) {
		out = append(out, bp)
	}
	sort.Strings(out)
	return out
}
