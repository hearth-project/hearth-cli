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
	for bp, p := range files {
		b, err := os.ReadFile(p)
		if err != nil || len(b) == 0 {
			continue
		}
		if s, ok := have[bp]; ok && bytes.Equal(s.Value, b) {
			continue
		}
		if err := e.SetSecret(Secret{Name: libSecretName(bp), Value: b, RestoreTo: libRestorePrefix + bp}); err != nil {
			return n, err
		}
		n++
	}
	for bp, s := range have {
		if _, ok := files[bp]; ok || had[bp] == "" {
			continue // still here, or never here (another machine's upload)
		}
		// Gone from this library since the last sync (deleted, or no
		// longer a credential).
		_ = e.DeleteSecret(s.Name)
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
func (e *Engine) restoreLibSecrets() (int, error) {
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
