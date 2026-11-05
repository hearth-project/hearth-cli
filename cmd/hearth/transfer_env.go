package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/datatool"
	"github.com/hearth-project/hearth-cli/internal/library"
)

// transferEnv is what the transfer commands share: the [transfer] settings
// of config.toml, a client for the service, and the local task records.
type transferEnv struct {
	cfg   library.TransferConfig
	cl    *datatool.Client
	store *datatool.Store
	user  string
}

const transferSetupHint = "hearth transfer is not set up: add a [transfer] section to config.toml (hearth config --edit):\n" +
	"  [transfer]\n" +
	"  url = \"http://datatool.example.com\"\n" +
	"  token_secret = \"datatool_token\"   # optional: hearth secret set datatool_token\n" +
	"see docs/en/usage.md (Copying large data between storages)"

func loadTransferEnv() (*transferEnv, error) {
	home := client.Home()
	uc, err := client.UserConfig(home)
	if err != nil {
		return nil, err
	}
	if uc.Transfer == nil || strings.TrimSpace(uc.Transfer.URL) == "" {
		return nil, errors.New(transferSetupHint)
	}
	t := *uc.Transfer
	timeout := 30 * time.Second
	if t.Timeout != "" {
		if timeout, err = time.ParseDuration(t.Timeout); err != nil {
			return nil, fmt.Errorf("[transfer] timeout %q: %w", t.Timeout, err)
		}
	}
	token, err := transferToken(t)
	if err != nil {
		return nil, err
	}
	cl, err := datatool.New(t.URL, t.APIPrefix, token, timeout, t.UseProxy)
	if err != nil {
		return nil, err
	}
	env := &transferEnv{cfg: t, cl: cl, store: &datatool.Store{Path: filepath.Join(home, "transfers.json")}}
	if c, err := client.LoadConfig(home); err == nil {
		env.user = datatool.UserOf(c.Email)
	}
	return env, nil
}

// transferToken: the environment variable first, then the secret. Neither
// named: no token is sent.
func transferToken(t library.TransferConfig) (string, error) {
	if t.TokenEnv != "" {
		if v := strings.TrimSpace(os.Getenv(t.TokenEnv)); v != "" {
			return v, nil
		}
	}
	if t.TokenSecret == "" {
		if t.TokenEnv != "" {
			return "", fmt.Errorf("[transfer] token_env: $%s is empty (or set token_secret)", t.TokenEnv)
		}
		return "", nil
	}
	return transferSecret(t.TokenSecret, "token_secret")
}

func transferSecret(name, setting string) (string, error) {
	e, err := engine()
	if err != nil {
		return "", err
	}
	s, err := e.Secret(name)
	if err != nil {
		return "", fmt.Errorf("[transfer] %s: %w (create it: hearth secret set %s)", setting, err, name)
	}
	v := strings.TrimSpace(string(s.Value))
	if v == "" {
		return "", fmt.Errorf("[transfer] %s: secret %q is empty", setting, name)
	}
	return v, nil
}

// rules returns the path rewrite rules of one storage.
func (t *transferEnv) rules(storage string) []datatool.Rule {
	var out []datatool.Rule
	for _, r := range t.cfg.Paths[storage] {
		out = append(out, datatool.Rule{From: r.From, To: r.To})
	}
	return out
}

// parseStoragePath reads STORAGE:PATH. A path that matches a rewrite rule
// of that storage is translated; a URL (afs://...) and a path without a
// matching rule go to the service as they are. A relative path is made
// absolute first, so a rule can translate it.
func (t *transferEnv) parseStoragePath(storages []string, arg string) (sp datatool.StoragePath, mapped bool, err error) {
	for _, s := range storages {
		p, ok := strings.CutPrefix(arg, s+":")
		if !ok {
			continue
		}
		if p == "" {
			return sp, false, fmt.Errorf("%q has no path after %s:", arg, s)
		}
		rel := !strings.Contains(p, "://") && !filepath.IsAbs(p)
		if rel {
			for _, seg := range strings.Split(p, "/") {
				if seg == ".." {
					return sp, false, fmt.Errorf("%q has a .. segment; give the path without it", arg)
				}
			}
			if p, err = filepath.Abs(p); err != nil {
				return sp, false, err
			}
		}
		out, mapped, err := datatool.MapPath(t.rules(s), p, t.user)
		if err != nil {
			return sp, false, err
		}
		if rel && !mapped {
			return sp, false, fmt.Errorf("%q is relative to this machine and matches no [transfer.paths.%q] rule (resolved: %s); give the storage path instead", arg, s, out)
		}
		return datatool.StoragePath{Storage: s, Path: out}, mapped, nil
	}
	return sp, false, fmt.Errorf("%q: expected STORAGE:PATH with STORAGE one of %s", arg, strings.Join(storages, ", "))
}
