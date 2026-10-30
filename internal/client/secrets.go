package client

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/hearth-project/hearth-cli/internal/paths"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/hearth-project/hearth-cli/internal/api"
)

// Secret is a decrypted credential. RestoreTo makes it a file secret that
// `secret restore` (and `setup`) writes back with mode 0600.
type Secret struct {
	Name      string `json:"name"`
	Value     []byte `json:"value"`
	RestoreTo string `json:"restore_to,omitempty"` // "~/..." or absolute
	UpdatedAt int64  `json:"updated_at"`
}

var secretNameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var envNameRE = regexp.MustCompile(`^[A-Z_][A-Z0-9_]*$`)

// IsEnvName reports whether a secret can be injected as an env variable.
func IsEnvName(n string) bool { return envNameRE.MatchString(n) }

func (a *API) Secrets() ([]api.Secret, error) {
	var r []api.Secret
	_, err := a.do("GET", "/v1/secrets", nil, nil, nil, &r)
	return r, err
}

func (a *API) PutSecret(id string, ct []byte) error {
	_, err := a.do("PUT", "/v1/secrets/"+url.PathEscape(id), api.PutSecretRequest{Ciphertext: ct}, nil, nil, nil)
	return err
}

func (a *API) DeleteSecret(id string) error {
	_, err := a.do("DELETE", "/v1/secrets/"+url.PathEscape(id), nil, nil, nil, nil)
	return err
}

// SetSecret encrypts and uploads a secret.
func (e *Engine) SetSecret(s Secret) error {
	if !secretNameRE.MatchString(s.Name) {
		return fmt.Errorf("invalid secret name %q", s.Name)
	}
	if len(s.Value) == 0 || len(s.Value) > api.MaxSecretBytes-1024 {
		return fmt.Errorf("secret value must be 1..%d bytes", api.MaxSecretBytes-1024)
	}
	if s.RestoreTo != "" && !paths.Portable(s.RestoreTo) && !strings.HasPrefix(s.RestoreTo, libRestorePrefix) {
		return errors.New("--restore-to must be absolute or start with ~/, ${HOME}/ or ${WORKSPACE}/")
	}
	s.UpdatedAt = time.Now().Unix()
	payload, err := json.Marshal(s)
	if err != nil {
		return err
	}
	id := e.Vault.SecretID(s.Name)
	ct, err := e.Vault.SealSecret(id, payload)
	if err != nil {
		return err
	}
	return e.API.PutSecret(id, ct)
}

// Secrets downloads and decrypts every secret, sorted by name.
func (e *Engine) Secrets() ([]Secret, error) {
	raw, err := e.API.Secrets()
	if err != nil {
		return nil, err
	}
	out := make([]Secret, 0, len(raw))
	for _, r := range raw {
		pt, err := e.Vault.OpenSecret(r.ID, r.Ciphertext)
		if err != nil {
			return nil, fmt.Errorf("secret %s…: %w", r.ID[:8], err)
		}
		var s Secret
		if err := json.Unmarshal(pt, &s); err != nil {
			return nil, err
		}
		if e.Vault.SecretID(s.Name) != r.ID {
			return nil, errors.New("secret name does not match its id (tampered?)")
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Secret returns one secret by name.
func (e *Engine) Secret(name string) (Secret, error) {
	all, err := e.Secrets()
	if err != nil {
		return Secret{}, err
	}
	for _, s := range all {
		if s.Name == name {
			return s, nil
		}
	}
	return Secret{}, fmt.Errorf("no secret named %q", name)
}

// DeleteSecret removes a secret on the server.
func (e *Engine) DeleteSecret(name string) error {
	err := e.API.DeleteSecret(e.Vault.SecretID(name))
	if StatusOf(err) == 404 {
		return fmt.Errorf("no secret named %q", name)
	}
	return err
}

// ExpandHome resolves a leading "~/".
func ExpandHome(p string) string { return paths.Expand(p) }

// RestoreResult reports one file secret.
type SecretRestore struct {
	Name, Path, Action string // written, unchanged, skipped
}

// RestoreSecretFiles writes file secrets to their paths (mode 0600). A
// different existing file is kept unless force is set.
func (e *Engine) RestoreSecretFiles(force bool) ([]SecretRestore, error) {
	all, err := e.Secrets()
	if err != nil {
		return nil, err
	}
	var out []SecretRestore
	for _, s := range all {
		if s.RestoreTo == "" || strings.HasPrefix(s.RestoreTo, libRestorePrefix) {
			continue // library credential files: written with their skill
		}
		p := ExpandHome(s.RestoreTo)
		r := SecretRestore{Name: s.Name, Path: p}
		cur, err := os.ReadFile(p)
		switch {
		case err == nil && bytes.Equal(cur, s.Value):
			r.Action = "unchanged"
		case err == nil && !force:
			r.Action = "skipped (exists with different content; use --force)"
		default:
			if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
				return out, err
			}
			if err := writeFileAtomic(p, s.Value, 0o600); err != nil {
				return out, err
			}
			r.Action = "written"
		}
		out = append(out, r)
	}
	return out, nil
}

// SecretEnv returns NAME=value pairs for env-style secrets without a file path.
func (e *Engine) SecretEnv() ([]string, error) {
	all, err := e.Secrets()
	if err != nil {
		return nil, err
	}
	var env []string
	for _, s := range all {
		if s.RestoreTo == "" && IsEnvName(s.Name) && !bytes.ContainsAny(s.Value, "\x00\n") {
			env = append(env, s.Name+"="+string(s.Value))
		}
	}
	return env, nil
}
