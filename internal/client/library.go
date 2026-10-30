package client

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/library"
	"github.com/hearth-project/hearth-cli/internal/paths"
)

// Library sync: the user's library is uploaded to their private space on
// the server as one versioned bundle (design doc 8.2: plaintext, owner-only).

// LibraryDir is the user's canonical library.
func LibraryDir(home string) string { return filepath.Join(home, "library") }

// RenderManifest records what `lib sync` rendered.
func RenderManifest(home string) string { return filepath.Join(home, "render.json") }

// BackupDir receives anything hearth replaces.
func BackupDir(home string) string { return filepath.Join(home, "backup") }

type libState struct {
	Version    int64  `json:"version"`
	SHA256     string `json:"sha256"`
	SecretsSHA string `json:"secrets_sha256,omitempty"` // credential files of skills (encrypted secrets)
}

func libStatePath(home string) string { return filepath.Join(home, "library-state.json") }

func loadLibState(home string) (libState, error) {
	var s libState
	err := loadJSON(libStatePath(home), &s)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	return s, err
}

// ErrLibraryConflict means both the local library and the server changed.
var ErrLibraryConflict = errors.New("library changed both locally and on the server")

// SecretsError lists likely credentials that blocked an upload.
type SecretsError struct{ Findings []library.Finding }

func (e *SecretsError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "refusing to upload: %d likely secret(s) in the library:", len(e.Findings))
	for _, f := range e.Findings {
		b.WriteString("\n  " + f.String())
	}
	b.WriteString("\nmove them to an environment variable, mark a documented example with `hearth:allow-secret`, or pass --allow-secrets")
	return b.String()
}

func (a *API) GetLibrary(known int64) (data []byte, version int64, sum string, err error) {
	hdr, err := a.do("GET", "/v1/library", nil, nil, map[string]string{api.HeaderLibraryVersion: strconv.FormatInt(known, 10)}, &data)
	if hdr != nil {
		version, _ = strconv.ParseInt(hdr.Get(api.HeaderLibraryVersion), 10, 64)
		sum = hdr.Get(api.HeaderLibrarySHA256)
	}
	return data, version, sum, err
}

func (a *API) PutLibrary(base int64, sum string, data []byte) (int64, error) {
	var r struct {
		Version int64 `json:"version"`
	}
	_, err := a.do("PUT", "/v1/library", nil, data, map[string]string{
		api.HeaderLibraryVersion: strconv.FormatInt(base, 10), api.HeaderLibrarySHA256: sum}, &r)
	return r.Version, err
}

// LibResult describes a library sync step.
type LibResult struct {
	Op      string // up-to-date, pushed, pulled, empty
	Version int64
}

// LibPush uploads the local library if it changed since the last sync.
// Credential files inside skills go up as encrypted secrets instead of in
// the bundle. When the server changed too, the two are merged.
func (e *Engine) LibPush(allowSecrets bool) (LibResult, error) {
	defer lockLibrary(e.Home)()
	return e.libPush(allowSecrets)
}

func (e *Engine) libPush(allowSecrets bool) (LibResult, error) {
	if _, err := EnsureUserConfig(e.Home); err != nil {
		return LibResult{}, err
	}
	st, err := loadLibState(e.Home)
	if err != nil {
		return LibResult{}, err
	}
	lib := library.Library{Root: LibraryDir(e.Home)}
	data, sum, secSum, secrets, err := e.libBundle()
	if err != nil {
		return LibResult{}, err
	}
	if sum == st.SHA256 && secSum == st.SecretsSHA {
		return LibResult{Op: "up-to-date", Version: st.Version}, nil
	}
	if !allowSecrets {
		// Outside the skills (config, recipes, files) a credential is a
		// mistake to fix, not a file to keep encrypted.
		fs, err := library.ScanSecrets(lib.Root)
		if err != nil {
			return LibResult{}, err
		}
		var bad []library.Finding
		for _, f := range fs {
			if !strings.HasPrefix(filepath.ToSlash(f.File), "skills/") {
				bad = append(bad, f)
			}
		}
		if len(bad) > 0 {
			return LibResult{}, &SecretsError{Findings: bad}
		}
	}
	if secSum != st.SecretsSHA {
		if _, err := e.pushLibSecrets(secrets); err != nil {
			return LibResult{}, fmt.Errorf("credential files of skills: %w", err)
		}
	}
	if sum == st.SHA256 {
		st.SecretsSHA = secSum
		return LibResult{Op: "pushed", Version: st.Version}, saveJSON(libStatePath(e.Home), st)
	}
	v, err := e.API.PutLibrary(st.Version, sum, data)
	if StatusOf(err) == 409 {
		return e.libMerge()
	}
	if err != nil {
		return LibResult{}, err
	}
	e.saveLibBase(data)
	return LibResult{Op: "pushed", Version: v}, saveJSON(libStatePath(e.Home), libState{Version: v, SHA256: sum, SecretsSHA: secSum})
}

// LibPull brings the server's latest library here. Local changes that
// were not pushed yet are merged with it (force: replaced); the replaced
// library is always moved to BackupDir first.
func (e *Engine) LibPull(force bool) (LibResult, error) {
	defer lockLibrary(e.Home)()
	return e.libPull(force)
}

func (e *Engine) libPull(force bool) (LibResult, error) {
	st, err := loadLibState(e.Home)
	if err != nil {
		return LibResult{}, err
	}
	lib := library.Library{Root: LibraryDir(e.Home)}
	_, localSum, secSum, _, err := e.libBundle()
	if err != nil {
		return LibResult{}, err
	}
	localChanged := localSum != st.SHA256 || secSum != st.SecretsSHA
	known := st.Version
	if localChanged {
		known = -1 // always fetch when local differs, to report precisely
	}
	data, v, sum, err := e.API.GetLibrary(known)
	switch StatusOf(err) {
	case 304:
		return LibResult{Op: "up-to-date", Version: st.Version}, nil
	case 404:
		return LibResult{Op: "empty"}, nil
	}
	if err != nil {
		return LibResult{}, err
	}
	if sum == localSum && !localChanged {
		return LibResult{Op: "up-to-date", Version: v}, nil
	}
	_, statErr := os.Stat(lib.Root)
	if localChanged && !force && !os.IsNotExist(statErr) {
		return e.libMerge()
	}
	tmp := lib.Root + ".incoming-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	got, err := library.Extract(data, tmp)
	if err != nil {
		os.RemoveAll(tmp)
		return LibResult{}, fmt.Errorf("extract library: %w", err)
	}
	if got != sum {
		os.RemoveAll(tmp)
		return LibResult{}, fmt.Errorf("library bundle hash mismatch")
	}
	if err := e.installLibrary(tmp); err != nil {
		return LibResult{}, err
	}
	e.saveLibBase(data)
	_, _ = e.restoreLibSecrets()
	_, _, secSum, _, _ = e.libBundle()
	return LibResult{Op: "pulled", Version: v}, saveJSON(libStatePath(e.Home), libState{Version: v, SHA256: sum, SecretsSHA: secSum})
}

// installLibrary makes the extracted tree tmp the local library. The
// bundle always carries skills/; config.toml decides whether they live in
// the library or in the user's own directory.
func (e *Engine) installLibrary(tmp string) error {
	lib := library.Library{Root: LibraryDir(e.Home)}
	newLib := library.Library{Root: tmp}
	newSkills := newLib.SkillsDir()
	extracted := filepath.Join(tmp, "skills")
	oldSkills := lib.SkillsDir()
	if filepath.Clean(newSkills) != filepath.Clean(extracted) {
		if _, err := os.Stat(extracted); err == nil {
			if err := moveSkills(extracted, newSkills, filepath.Join(BackupDir(e.Home), time.Now().Format("20060102-150405"))); err != nil {
				os.RemoveAll(tmp)
				return err
			}
		}
		if filepath.Clean(oldSkills) != filepath.Clean(newSkills) {
			if _, err := os.Stat(oldSkills); err == nil {
				bak := filepath.Join(BackupDir(e.Home), time.Now().Format("20060102-150405"), "skills-old")
				os.MkdirAll(filepath.Dir(bak), 0o700)
				os.Rename(oldSkills, bak)
			}
		}
	}
	if _, err := os.Stat(lib.Root); err == nil {
		bak := filepath.Join(BackupDir(e.Home), time.Now().Format("20060102-150405"), "library")
		for i := 2; fileExists(bak); i++ { // two syncs within a second
			bak = filepath.Join(BackupDir(e.Home), time.Now().Format("20060102-150405")+"-"+strconv.Itoa(i), "library")
		}
		if err := os.MkdirAll(filepath.Dir(bak), 0o700); err != nil {
			return err
		}
		if err := os.Rename(lib.Root, bak); err != nil {
			return err
		}
	}
	return os.Rename(tmp, lib.Root)
}

// SetSkillsDir moves the single skills copy to dir (portable path) and
// records it in <library>/config.toml, so the choice travels to the
// user's other machines. An empty dir shows the current setting.
func SetSkillsDir(home, dir string) (string, error) {
	lib := library.Library{Root: LibraryDir(home)}
	oldSkills := lib.SkillsDir()
	if dir == "" {
		return oldSkills, nil
	}
	if !paths.Portable(dir) {
		return "", fmt.Errorf("skills dir %q must be absolute or start with ~/, ${HOME}/ or ${WORKSPACE}/", dir)
	}
	target := filepath.Clean(paths.Expand(dir))
	root := filepath.Clean(lib.Root)
	if target == root || strings.HasPrefix(target, root+string(filepath.Separator)) {
		return "", fmt.Errorf("skills dir must be outside the library itself (%s); use the default layout instead", root)
	}
	cfg, err := lib.LoadConfig()
	if err != nil {
		return "", err
	}
	if cfg.Skills == nil {
		cfg.Skills = &library.SkillsConfig{}
	}
	cfg.Skills.Dir = dir
	if err := moveSkills(oldSkills, target, filepath.Join(BackupDir(home), time.Now().Format("20060102-150405"))); err != nil {
		return "", err
	}
	if err := lib.SaveConfig(cfg); err != nil {
		return "", err
	}
	return target, nil
}

// moveSkills merges src into dst: missing entries move over, entries dst
// already has are backed up first (a pull replaces local content).
func moveSkills(src, dst, backup string) error {
	if filepath.Clean(src) == filepath.Clean(dst) {
		return os.MkdirAll(dst, 0o755)
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	entries, err := os.ReadDir(src)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		from, to := filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())
		if _, err := os.Lstat(to); err == nil {
			if err := os.MkdirAll(backup, 0o700); err != nil {
				return err
			}
			bak := filepath.Join(backup, e.Name())
			os.RemoveAll(bak)
			if err := os.Rename(to, bak); err != nil {
				return err
			}
		}
		if err := os.Rename(from, to); err != nil {
			// Different filesystems: copy, then drop the source.
			if err := copyTree(from, to); err != nil {
				return err
			}
			os.RemoveAll(from)
		}
	}
	os.Remove(src) // only if now empty
	return nil
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(to, 0o755)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(to, b, info.Mode().Perm())
	})
}

// LibAuto is run periodically by the daemon: push local edits, pull and
// render remote edits; when both changed, merge them.
func (e *Engine) LibAuto() (LibResult, error) {
	r, err := e.libAuto()
	e.recordLibError(err)
	return r, err
}

func (e *Engine) libAuto() (LibResult, error) {
	defer lockLibrary(e.Home)()
	st, err := loadLibState(e.Home)
	if err != nil {
		return LibResult{}, err
	}
	_, localSum, secSum, _, err := e.libBundle()
	if err != nil {
		return LibResult{}, err
	}
	_, statErr := os.Stat(LibraryDir(e.Home))
	neverSynced := st.SHA256 == "" && os.IsNotExist(statErr)
	var r LibResult
	if (localSum != st.SHA256 || secSum != st.SecretsSHA) && !neverSynced {
		r, err = e.libPush(false) // a conflict is merged
	} else {
		r, err = e.libPull(false)
	}
	if err != nil {
		return r, err
	}
	// Every pass: agent homes may have moved (hearth agents isolate) and
	// residents changed; rendering an unchanged library changes nothing.
	_, err = e.Render(false)
	return r, err
}

// libErrorPath remembers the last automatic library sync failure. The
// daemon logs a conflict once and then stays quiet, so `doctor` reads this
// to keep telling the user why their recipes and secrets stopped syncing.
func libErrorPath(home string) string { return filepath.Join(home, "library-error.json") }

type libError struct {
	Error string `json:"error"`
	Time  int64  `json:"time"`
}

func (e *Engine) recordLibError(err error) {
	if err == nil {
		os.Remove(libErrorPath(e.Home))
		return
	}
	_ = saveJSON(libErrorPath(e.Home), libError{Error: err.Error(), Time: time.Now().Unix()})
}

// LibraryError returns the last automatic library sync failure, if any.
func LibraryError(home string) (msg string, when time.Time, ok bool) {
	var le libError
	if err := loadJSON(libErrorPath(home), &le); err != nil || le.Error == "" {
		return "", time.Time{}, false
	}
	return le.Error, time.Unix(le.Time, 0), true
}

// Render renders the library plus linked shared items into every agent.
func (e *Engine) Render(adopt bool) ([]library.Action, error) {
	return e.RenderOpts(library.Options{Adopt: adopt})
}

// RenderOpts is Render with explicit options (Manifest/BackupDir/Extra are filled in).
func (e *Engine) RenderOpts(opt library.Options) ([]library.Action, error) {
	ex, problems := e.Linked()
	opt.Manifest, opt.BackupDir, opt.Extra = RenderManifest(e.Home), BackupDir(e.Home), ex
	if opt.Present == nil {
		opt.Present = AgentSkillDirs(e.Home).Has
	}
	// The enabled agents are a library setting: a pull may just have
	// changed them (a new machine starts with the defaults). Targets set
	// explicitly by the caller are kept.
	if sameTargets(e.LibTargets, e.autoTargets) {
		e.LibTargets = EnabledLibTargets(e.Home)
		e.autoTargets = e.LibTargets
	}
	lib := library.Library{Root: LibraryDir(e.Home)}
	acts, err := library.Render(lib, e.LibTargets, opt)
	if _, cerr := lib.LoadConfig(); cerr != nil {
		acts = append(acts, library.Action{Op: "warn", Item: "config", Detail: cerr.Error()})
	}
	for _, p := range problems {
		acts = append(acts, library.Action{Op: "warn", Detail: p})
	}
	if err != nil || opt.DryRun {
		return acts, err
	}
	// MCP registrations are part of the library too.
	macts, merr := e.RenderMCP(opt.Adopt)
	for _, m := range macts {
		if m.Op != "unchanged" {
			acts = append(acts, library.Action{Path: m.Detail, Item: "mcp:" + m.Agent + "/" + m.Server, Op: m.Op})
		}
	}
	if merr != nil {
		acts = append(acts, library.Action{Item: "mcp", Op: "warn", Detail: merr.Error()})
	}
	return acts, nil
}

func sameTargets(a, b []library.Target) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Name != b[i].Name || a[i].Root != b[i].Root {
			return false
		}
	}
	return true
}
