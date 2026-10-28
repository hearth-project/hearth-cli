package server

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"

	"github.com/pelletier/go-toml/v2"

	"github.com/hearth-project/hearth-cli/internal/library"
)

// Details for the console: the full text and files of a library item or a
// team package, the user's settings and links.

type fileEntry struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type itemDetail struct {
	Type    string      `json:"type"`
	Name    string      `json:"name"`
	Content string      `json:"content"` // SKILL.md or the agent file (first 256 KiB)
	Files   []fileEntry `json:"files"`
}

// readItem reads one item from a bundle: prefix "" for a package bundle
// (SKILL.md / AGENT.md at the top), or "skills/<name>/" / "agents/<name>.md".
func readItem(data []byte, typ, name string, pkg bool) (itemDetail, error) {
	d := itemDetail{Type: typ, Name: name, Files: []fileEntry{}}
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return d, err
	}
	tr := tar.NewReader(zr)
	found := false
	for n := 0; n < 50000; n++ {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return d, err
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		p := path.Clean(hdr.Name)
		rel, main := "", false
		switch {
		case pkg:
			rel, main = p, p == "SKILL.md" || p == "AGENT.md"
		case typ == "skill" && strings.HasPrefix(p, "skills/"+name+"/"):
			rel = strings.TrimPrefix(p, "skills/"+name+"/")
			main = rel == "SKILL.md"
		case typ == "agent" && p == "agents/"+name+".md":
			rel, main = name+".md", true
		default:
			continue
		}
		found = true
		d.Files = append(d.Files, fileEntry{rel, hdr.Size})
		if main {
			b, _ := io.ReadAll(io.LimitReader(tr, 256<<10))
			d.Content = string(b)
		}
	}
	if !found {
		return d, errNotFound
	}
	return d, nil
}

func (s *Server) myBundle(uid int64) (libraryRow, []byte, error) {
	lib, err := s.st.getLibrary(uid)
	if err != nil {
		return lib, nil, err
	}
	data, err := s.st.readObject(lib.Object)
	return lib, data, err
}

func (s *Server) apiMyItem(w http.ResponseWriter, r *http.Request, c userCtx) {
	_, data, err := s.myBundle(c.uid)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	d, err := readItem(data, r.PathValue("type"), r.PathValue("name"), false)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

type pkgVersion struct {
	Version     int64  `json:"version"`
	PublishedBy string `json:"published_by"`
	PublishedAt int64  `json:"published_at"`
	Description string `json:"description"`
}

func (s *Server) apiTeamItem(w http.ResponseWriter, r *http.Request, c userCtx) {
	o, err := s.st.membership(c.uid, r.PathValue("team"), "reader")
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	typ, name, ok := pkgPath(r)
	if !ok {
		writeErr(w, http.StatusBadRequest, "invalid package")
		return
	}
	p, err := s.st.getPackage(o.ID, typ, name, 0)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	data, err := s.st.readObject(p.object)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	d, err := readItem(data, typ, name, true)
	if err != nil {
		s.writeStoreErr(w, err)
		return
	}
	versions := []pkgVersion{}
	if rows, err := s.st.rdb.Query(`SELECT v.version, COALESCE(u.email,''), v.published_at, v.description FROM package_versions v
		JOIN packages k ON k.id=v.package_id LEFT JOIN users u ON u.id=v.published_by
		WHERE k.team_id=? AND k.type=? AND k.name=? ORDER BY v.version DESC LIMIT 50`, o.ID, typ, name); err == nil {
		for rows.Next() {
			var v pkgVersion
			if rows.Scan(&v.Version, &v.PublishedBy, &v.PublishedAt, &v.Description) == nil {
				versions = append(versions, v)
			}
		}
		rows.Close()
	}
	linked := false
	_, bundle, err := s.myBundle(c.uid)
	if err == nil {
		linked = bundleHasLink(bundle, typ, name)
	}
	writeJSON(w, http.StatusOK, map[string]any{"item": d, "versions": versions, "latest": p.Version, "linked": linked})
}

// bundleFile returns one file of a library bundle (nil if absent).
func bundleFile(data []byte, name string) []byte {
	zr, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil
	}
	tr := tar.NewReader(zr)
	for {
		hdr, err := tr.Next()
		if err != nil {
			return nil
		}
		if path.Clean(hdr.Name) == name && hdr.Typeflag == tar.TypeReg {
			b, _ := io.ReadAll(io.LimitReader(tr, 1<<20))
			return b
		}
	}
}

type bundleLink struct {
	Type    string `json:"type"`
	Name    string `json:"name"`
	Source  string `json:"source"`
	Version int64  `json:"version,omitempty"`
	Auto    bool   `json:"auto,omitempty"` // one of the team's most used, kept automatically
}

// bundleLinks reads the library's links.json (shared items kept in every
// agent).
func bundleLinks(data []byte) []bundleLink {
	var f struct {
		Links []bundleLink `json:"links"`
	}
	if b := bundleFile(data, "links.json"); b != nil {
		_ = json.Unmarshal(b, &f)
	}
	if f.Links == nil {
		return []bundleLink{}
	}
	return f.Links
}

func bundleHasLink(data []byte, typ, name string) bool {
	for _, l := range bundleLinks(data) {
		if l.Type == typ && l.Name == name {
			return true
		}
	}
	return false
}

// apiMySettings: the console-editable settings, read from the library's
// config.toml (changes go through intents).
func (s *Server) apiMySettings(w http.ResponseWriter, _ *http.Request, c userCtx) {
	out := map[string]any{"resident": []string{}, "share_top": 5, "share_exclude": []string{}, "links": []any{}}
	if _, data, err := s.myBundle(c.uid); err == nil {
		var cfg library.Config
		if b := bundleFile(data, "config.toml"); b != nil && toml.Unmarshal(b, &cfg) == nil {
			if cfg.Skills != nil && cfg.Skills.Resident != nil {
				out["resident"] = cfg.Skills.Resident
			}
			if cfg.Teams != nil {
				if cfg.Teams.ShareTop != nil {
					out["share_top"] = *cfg.Teams.ShareTop
				}
				if cfg.Teams.ShareExclude != nil {
					out["share_exclude"] = cfg.Teams.ShareExclude
				}
			}
		}
		out["links"] = bundleLinks(data)
	}
	writeJSON(w, http.StatusOK, out)
}
