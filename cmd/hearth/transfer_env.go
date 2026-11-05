package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/hearth-project/hearth-cli/internal/api"
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

const transferSetupHint = "hearth transfer is not set up. The server admin can provide the settings for everyone (transfer.json in the\n" +
	"server's data directory), or you set them yourself under [transfer] in config.toml (hearth config --edit):\n" +
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
	var local library.TransferConfig
	if uc.Transfer != nil {
		local = *uc.Transfer
	}
	// The server's settings are the default; the user's own win.
	var srv *api.TransferConfig
	e, srvErr := engine()
	if srvErr == nil {
		srv, srvErr = e.API.TransferConfig()
	}
	t, srvToken := mergeTransfer(local, srv)
	if strings.TrimSpace(t.URL) == "" {
		if srvErr != nil {
			return nil, fmt.Errorf("%s\n(the server's transfer settings could not be read: %w)", transferSetupHint, srvErr)
		}
		return nil, errors.New(transferSetupHint)
	}
	if srvErr != nil {
		fmt.Fprintf(os.Stderr, "warning: the server's transfer settings could not be read (%v); using this machine's settings only\n", srvErr)
	}
	timeout := 30 * time.Second
	if t.Timeout != "" {
		if timeout, err = time.ParseDuration(t.Timeout); err != nil {
			return nil, fmt.Errorf("[transfer] timeout %q: %w", t.Timeout, err)
		}
	}
	token, err := transferToken(t, srvToken)
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

// mergeTransfer lays the user's settings over the server's. Scalars the user
// set win; the path rules of a storage the user lists replace the server's
// for that storage. The server's token is returned apart and is used only
// for the server's own address: pointing url elsewhere must not send it
// there.
func mergeTransfer(local library.TransferConfig, srv *api.TransferConfig) (library.TransferConfig, string) {
	t := local
	if srv == nil {
		return t, ""
	}
	token := ""
	if local.URL == "" || sameTransferURL(local.URL, srv.URL) {
		token = srv.Token
	}
	if t.URL == "" {
		t.URL = srv.URL
	}
	if t.APIPrefix == "" {
		t.APIPrefix = srv.APIPrefix
	}
	if t.Timeout == "" {
		t.Timeout = srv.Timeout
	}
	if t.AFSUGISecret == "" {
		t.AFSUGISecret = srv.AFSUGISecret
	}
	if len(srv.Paths) > 0 {
		paths := map[string][]library.TransferPathRule{}
		for storage, rules := range srv.Paths {
			for _, r := range rules {
				paths[storage] = append(paths[storage], library.TransferPathRule{From: r.From, To: r.To})
			}
		}
		for storage, rules := range local.Paths {
			paths[storage] = rules
		}
		t.Paths = paths
	}
	return t, token
}

func sameTransferURL(a, b string) bool {
	return strings.TrimRight(strings.TrimSpace(a), "/") == strings.TrimRight(strings.TrimSpace(b), "/")
}

// transferToken: the user's environment variable, then the user's secret,
// then the server's token (fromServer). None: no token is sent.
func transferToken(t library.TransferConfig, fromServer string) (string, error) {
	if t.TokenEnv != "" {
		if v := strings.TrimSpace(os.Getenv(t.TokenEnv)); v != "" {
			return v, nil
		}
	}
	if t.TokenSecret == "" {
		if fromServer != "" {
			return fromServer, nil
		}
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
