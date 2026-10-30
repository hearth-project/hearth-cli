package client

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// Library merge: the library changed here and on the server (another
// machine, the web console). Instead of stopping the sync until the user
// runs `hearth lib pull`, both are merged file by file against the last
// synced version (the base): a file changed on one side only takes that
// side (deletions too); a file changed on both keeps this machine's copy
// and the server's goes to the backup dir. Without a base (first merge
// after an upgrade) nothing is deleted: files of either side are kept.

func libBasePath(home string) string { return filepath.Join(home, "library-base.bundle") }

// saveLibBase keeps the bundle just synced, the base of the next merge.
func (e *Engine) saveLibBase(data []byte) { _ = writeFileAtomic(libBasePath(e.Home), data, 0o600) }

// LibMergeResult is the last merge, for the log and `hearth lib status`.
type LibMergeResult struct {
	FromServer, Kept, Conflicts []string
}

// LastLibMerge is the most recent merge of this process.
var LastLibMerge *LibMergeResult

var libMerging bool

func fileHashes(dir string) map[string]string {
	out := map[string]string{}
	if dir == "" {
		return out
	}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return nil
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		h := sha256.Sum256(b)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(h[:])
		return nil
	})
	return out
}

func (e *Engine) libMerge() (LibResult, error) {
	if libMerging {
		// The server changed again while this merge was pushed.
		return LibResult{}, fmt.Errorf("%w: the server changed again during a merge; the next sync retries", ErrLibraryConflict)
	}
	libMerging = true
	defer func() { libMerging = false }()
	st, err := loadLibState(e.Home)
	if err != nil {
		return LibResult{}, err
	}
	remote, v, rsum, err := e.API.GetLibrary(-1)
	if err != nil {
		return LibResult{}, err
	}
	local, _, _, secrets, err := e.libBundle()
	if err != nil {
		return LibResult{}, err
	}
	work, err := os.MkdirTemp(e.Home, "library-merge-")
	if err != nil {
		return LibResult{}, err
	}
	defer os.RemoveAll(work)
	dirs := map[string]string{}
	for name, data := range map[string][]byte{"local": local, "remote": remote} {
		d := filepath.Join(work, name)
		if _, err := library.Extract(data, d); err != nil {
			return LibResult{}, fmt.Errorf("merge: %s library: %w", name, err)
		}
		dirs[name] = d
	}
	hasBase := false
	if base, err := os.ReadFile(libBasePath(e.Home)); err == nil {
		if _, err := library.Extract(base, filepath.Join(work, "base")); err == nil {
			dirs["base"], hasBase = filepath.Join(work, "base"), true
		}
	}
	b, l, r := fileHashes(dirs["base"]), fileHashes(dirs["local"]), fileHashes(dirs["remote"])
	all := map[string]bool{}
	for _, m := range []map[string]string{b, l, r} {
		for k := range m {
			all[k] = true
		}
	}
	paths := make([]string, 0, len(all))
	for k := range all {
		paths = append(paths, k)
	}
	sort.Strings(paths)
	merged := e.Home + string(filepath.Separator) + "library.merged-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if err := os.MkdirAll(merged, 0o755); err != nil {
		return LibResult{}, err
	}
	bak := filepath.Join(BackupDir(e.Home), time.Now().Format("20060102-150405"), "merge-server-copies")
	res := &LibMergeResult{}
	take := func(side, rel string) error {
		if side == "" {
			return nil
		}
		_, err := copyHome(filepath.Join(dirs[side], filepath.FromSlash(rel)), filepath.Join(merged, filepath.FromSlash(rel)), "", "")
		return err
	}
	for _, rel := range paths {
		bh, lh, rh := b[rel], l[rel], r[rel]
		side := ""
		switch {
		case lh == rh:
			side = pick(lh != "", "local")
		case hasBase && lh == bh:
			side = pick(rh != "", "remote")
			res.FromServer = append(res.FromServer, rel)
		case hasBase && rh == bh:
			side = pick(lh != "", "local")
			res.Kept = append(res.Kept, rel)
		case lh == "":
			side = "remote" // no base: keep what either side has
			res.FromServer = append(res.FromServer, rel)
		case rh == "":
			side = "local"
			res.Kept = append(res.Kept, rel)
		default:
			side = "local"
			res.Conflicts = append(res.Conflicts, rel)
			if _, err := copyHome(filepath.Join(dirs["remote"], filepath.FromSlash(rel)), filepath.Join(bak, filepath.FromSlash(rel)), "", ""); err != nil {
				os.RemoveAll(merged)
				return LibResult{}, err
			}
		}
		if err := take(side, rel); err != nil {
			os.RemoveAll(merged)
			return LibResult{}, err
		}
	}
	// Credential files are not in the bundles: carry this machine's over.
	for bp, p := range secrets {
		if _, err := copyHome(p, filepath.Join(merged, filepath.FromSlash(bp)), "", ""); err != nil {
			os.RemoveAll(merged)
			return LibResult{}, err
		}
	}
	if err := e.installLibrary(merged); err != nil {
		os.RemoveAll(merged)
		return LibResult{}, err
	}
	_, _ = e.restoreLibSecrets()
	// The server's version is the base of the push that follows.
	e.saveLibBase(remote)
	st.Version, st.SHA256 = v, rsum
	if err := saveJSON(libStatePath(e.Home), st); err != nil {
		return LibResult{}, err
	}
	LastLibMerge = res
	if e.Logf != nil {
		e.Logf("library: merged with the server's version %d: %d file(s) from the server, %d kept from here, %d changed on both (this machine's kept; the server's in %s)",
			v, len(res.FromServer), len(res.Kept), len(res.Conflicts), bak)
	}
	pr, err := e.LibPush(false)
	if err != nil {
		return pr, err
	}
	pr.Op = "merged"
	return pr, nil
}

func pick(ok bool, side string) string {
	if ok {
		return side
	}
	return ""
}

// mergeSummary is one line for people.
func (m *LibMergeResult) String() string {
	parts := []string{fmt.Sprintf("%d from the server", len(m.FromServer)), fmt.Sprintf("%d kept from here", len(m.Kept))}
	if len(m.Conflicts) > 0 {
		parts = append(parts, fmt.Sprintf("%d changed on both (kept this machine's: %s)", len(m.Conflicts), strings.Join(m.Conflicts, ", ")))
	}
	return strings.Join(parts, ", ")
}
