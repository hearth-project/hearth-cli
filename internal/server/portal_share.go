package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// One-click sharing from the console: the server takes the skill or agent
// out of the user's library bundle (plaintext on the server already), runs
// the same secret scan as `hearth team share`, packs it exactly like the
// client does and publishes it to the chosen teams.

type shareResult struct {
	Team    string `json:"team"`
	Version int64  `json:"version,omitempty"`
	Created bool   `json:"created"`
	Error   string `json:"error,omitempty"`
}

// extractItem writes a library item into dir: a skill's files, or an
// agent as AGENT.md (the package layout).
func extractItem(bundle []byte, typ, name, dir string) error {
	zr, err := gzip.NewReader(bytes.NewReader(bundle))
	if err != nil {
		return err
	}
	tr := tar.NewReader(zr)
	prefix := "skills/" + name + "/"
	found, total := false, int64(0)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		p := path.Clean(hdr.Name)
		var rel string
		switch {
		case typ == "skill" && strings.HasPrefix(p+"/", prefix) && p+"/" != prefix:
			rel = strings.TrimPrefix(p, prefix)
		case typ == "agent" && p == "agents/"+name+".md":
			rel = "AGENT.md"
		default:
			continue
		}
		if strings.Contains(rel, "..") || path.IsAbs(rel) {
			return fmt.Errorf("bad path %q in the library", hdr.Name)
		}
		dst := filepath.Join(dir, filepath.FromSlash(rel))
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if total += hdr.Size; total > 64<<20 {
				return errors.New("item larger than 64 MiB")
			}
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			mode := os.FileMode(0o644)
			if hdr.Mode&0o111 != 0 {
				mode = 0o755
			}
			b, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
			if err != nil {
				return err
			}
			if err := os.WriteFile(dst, b, mode); err != nil {
				return err
			}
			found = true
		}
	}
	if !found {
		return fmt.Errorf("%w: %s %s is not in your library", errNotFound, typ, name)
	}
	return nil
}

func (s *Server) apiShareItem(w http.ResponseWriter, r *http.Request, c userCtx) {
	var req struct {
		Type  string   `json:"type"`
		Name  string   `json:"name"`
		Teams []string `json:"teams"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if (req.Type != "skill" && req.Type != "agent") || !validPkgName(req.Name) || len(req.Teams) == 0 || len(req.Teams) > 50 {
		writeErr(w, http.StatusBadRequest, "give type (skill or agent), name and teams")
		return
	}
	lib, err := s.st.getLibrary(c.uid)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	bundle, err := s.st.readObject(lib.Object)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	dir, err := os.MkdirTemp("", "hearth-share-")
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	defer os.RemoveAll(dir)
	if err := extractItem(bundle, req.Type, req.Name, dir); err != nil {
		s.writeStoreErr(w, err)
		return
	}
	if fs, err := library.ScanSecrets(dir); err != nil {
		s.writeStoreErr(w, err)
		return
	} else if len(fs) > 0 {
		var where []string
		for _, f := range fs {
			rel, _ := filepath.Rel(dir, f.File)
			where = append(where, fmt.Sprintf("%s:%d %s", rel, f.Line, f.Rule))
		}
		writeErr(w, http.StatusUnprocessableEntity, "possible credentials found ("+strings.Join(where, ", ")+"); remove them, or share from a terminal: hearth team share --allow-secrets")
		return
	}
	desc := ""
	if req.Type == "skill" {
		b, _ := os.ReadFile(filepath.Join(dir, "SKILL.md"))
		desc = library.SkillDescription(b)
	} else {
		b, _ := os.ReadFile(filepath.Join(dir, "AGENT.md"))
		if a, err := library.ParseAgent(b); err == nil {
			desc = a.Description
		}
	}
	data, sum, err := library.Library{Root: dir}.Bundle()
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	var out []shareResult
	for _, team := range req.Teams {
		res := shareResult{Team: team}
		o, err := s.st.membership(c.uid, team, "maintainer")
		if err == nil {
			res.Version, res.Created, err = s.st.publish(o.ID, c.uid, req.Type, req.Name, desc, sum, data, packageText(data), s.now())
			if err == nil && res.Created {
				s.search.invalidate(o.ID)
			}
		}
		switch {
		case errors.Is(err, errForbidden):
			res.Error = "members cannot share in this team (maintainer or owner needed)"
		case errors.Is(err, errNotFound):
			res.Error = "no such team"
		case err != nil:
			res.Error = err.Error()
		}
		out = append(out, res)
	}
	writeJSON(w, http.StatusOK, out)
}
