package client

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Site defaults published on the server reach clients: recipes and their
// files are used below the user's own library, learn.json adds token globs.
func TestSiteDefaults(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	site := filepath.Join(en.data, "site")
	os.MkdirAll(filepath.Join(site, "recipes"), 0o755)
	os.MkdirAll(filepath.Join(site, "files"), 0o755)
	os.WriteFile(filepath.Join(site, "recipes", "corp.toml"), []byte(`name = "corp"
description = "company defaults"

[[step]]
type = "file"
name = "corp-ssh"
library = "files/corp_ssh_config"
to = ["~/.ssh/corp_config"]
`), 0o644)
	os.WriteFile(filepath.Join(site, "files", "corp_ssh_config"), []byte("Host git.corp\n"), 0o644)
	os.WriteFile(filepath.Join(site, "learn.json"), []byte(`{"token_files":["~/.config/corp/token_*"]}`), 0o644)

	// Before login (the installer): no token needed.
	fresh := t.TempDir()
	if r, err := SiteSyncFrom(fresh, en.url); err != nil || r.Op != "pulled" {
		t.Fatalf("pull before login: %+v %v", r, err)
	}
	if _, err := os.Stat(filepath.Join(SiteDir(fresh), "recipes", "corp.toml")); err != nil {
		t.Fatal(err)
	}
	if r, err := a.SiteSync(); err != nil || r.Op != "pulled" {
		t.Fatalf("pull: %+v %v", r, err)
	}
	if r, err := a.SiteSync(); err != nil || r.Op != "up-to-date" {
		t.Fatalf("second pull: %+v %v", r, err)
	}
	rs, err := LoadRecipes(a.Home, nil)
	if err != nil || len(rs) != 1 || rs[0].Name != "corp" {
		t.Fatalf("recipes: %v %v", rs, err)
	}
	host := &recipeHost{e: a.Engine}
	if b, err := host.LibraryFile("files/corp_ssh_config"); err != nil || string(b) != "Host git.corp\n" {
		t.Fatalf("site file: %q %v", b, err)
	}
	if got := learnConfig(a.Home).TokenFiles; len(got) != 1 || got[0] != "~/.config/corp/token_*" {
		t.Fatalf("learn: %v", got)
	}

	// Opting out of a site recipe.
	if err := SetSiteRecipe(a.Home, "corp", false); err != nil {
		t.Fatal(err)
	}
	if rs, _ := LoadRecipes(a.Home, nil); len(rs) != 0 {
		t.Fatalf("disabled site recipe still loaded: %v", rs)
	}
	SetSiteRecipe(a.Home, "corp", true)

	// The user's own recipe of the same name wins.
	os.MkdirAll(RecipesDir(a.Home), 0o755)
	os.WriteFile(filepath.Join(RecipesDir(a.Home), "corp.toml"), []byte("name = \"corp\"\ndescription = \"mine\"\n"), 0o644)
	if rs, _ := LoadRecipes(a.Home, nil); len(rs) != 1 || rs[0].Description != "mine" {
		t.Fatalf("override: %+v", rs[0])
	}

	// Removed on the server: removed here (after the server's 10 s cache).
	os.RemoveAll(site)
	en.clock.add(time.Minute)
	if r, err := a.SiteSync(); err != nil || r.Op != "removed" {
		t.Fatalf("removed: %+v %v", r, err)
	}
}
