package datatool

import (
	"strings"
	"testing"
)

func TestMapPath(t *testing.T) {
	rules := []Rule{
		{From: "/mnt/personal", To: "/data/{user}"},
		{From: "/mnt", To: "/data"},
	}
	for _, c := range []struct {
		in, want string
		mapped   bool
	}{
		{"/mnt/personal/models/a", "/data/alice/models/a", true},
		{"/mnt/personal", "/data/alice", true},
		{"/mnt/personal/", "/data/alice", true},
		{"/mnt/shared/x", "/data/shared/x", true},
		{"/mnt/personal/./x//y", "/data/alice/x/y", true},       // cleaned before matching
		{"/mnt/personalities/x", "/data/personalities/x", true}, // only whole segments match the first rule
		{"/other/x", "/other/x", false},
		{"afs://cluster/user/dir", "afs://cluster/user/dir", false},
	} {
		got, mapped, err := MapPath(rules, c.in, "alice")
		if err != nil || got != c.want || mapped != c.mapped {
			t.Errorf("MapPath(%q) = %q, %v, %v; want %q, %v", c.in, got, mapped, err, c.want, c.mapped)
		}
	}
}

func TestMapPathRefusals(t *testing.T) {
	rules := []Rule{{From: "/mnt/personal", To: "/data/{user}"}}
	if _, _, err := MapPath(rules, "relative/dir", "alice"); err == nil {
		t.Error("a relative path was accepted")
	}
	for _, bad := range []string{"/mnt/personal/../shared", "/mnt/personal/x/..", "/a/../etc"} {
		if _, _, err := MapPath(rules, bad, "alice"); err == nil {
			t.Errorf("%q: a .. segment was resolved silently", bad)
		}
	}
	for _, bad := range []string{"", "../etc", "a/b", ".hidden", "al ice", "x\ny"} {
		if _, _, err := MapPath(rules, "/mnt/personal/x", bad); err == nil {
			t.Errorf("user %q accepted", bad)
		}
	}
	if UserOf("alice@example.com") != "alice" || UserOf("bob") != "bob" {
		t.Error("UserOf")
	}
}

func testMeta() *Meta {
	return &Meta{
		ValidPairs: []Pair{{"a@one", "b@two"}, {"b@two", "a@one"}},
		Limits:     Limits{NameMaxLen: 10, PathMaxLen: 30, TransfersMax: 32, CheckersMax: 64},
	}
}

func goodRequest() CreateRequest {
	return CreateRequest{Name: "model",
		Src: StoragePath{"a@one", "/data/alice/m"}, Dst: StoragePath{"b@two", "/data/alice/m"},
		Options: Options{Transfers: 8, Checkers: 16}}
}

func TestValidate(t *testing.T) {
	if err := Validate(testMeta(), goodRequest()); err != nil {
		t.Fatal(err)
	}
	for name, mut := range map[string]func(*CreateRequest){
		"empty name":      func(r *CreateRequest) { r.Name = "  " },
		"long name":       func(r *CreateRequest) { r.Name = strings.Repeat("n", 11) },
		"control in name": func(r *CreateRequest) { r.Name = "a\nb" },
		"no source path":  func(r *CreateRequest) { r.Src.Path = "" },
		"long path":       func(r *CreateRequest) { r.Dst.Path = "/" + strings.Repeat("p", 40) },
		"dotdot":          func(r *CreateRequest) { r.Dst.Path = "/data/../etc" },
		"same storage":    func(r *CreateRequest) { r.Dst.Storage = "a@one" },
		"unknown pair":    func(r *CreateRequest) { r.Dst.Storage = "c@three" },
		"transfers":       func(r *CreateRequest) { r.Options.Transfers = 33 },
		"checkers":        func(r *CreateRequest) { r.Options.Checkers = 65 },
		"negative":        func(r *CreateRequest) { r.Options.Transfers = -1 },
	} {
		r := goodRequest()
		mut(&r)
		if err := Validate(testMeta(), r); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestNeedsAFSUGI(t *testing.T) {
	if !NeedsAFSUGI("afs@cn", "weka@johor") || !NeedsAFSUGI("weka@johor", "afs@cn") || NeedsAFSUGI("gpfs@cn", "weka@johor") {
		t.Error("NeedsAFSUGI")
	}
}

func TestStore(t *testing.T) {
	s := &Store{Path: t.TempDir() + "/sub/transfers.json"}
	if recs, err := s.List(); err != nil || len(recs) != 0 || s.Has("x") {
		t.Fatalf("empty store: %v %v", recs, err)
	}
	for _, id := range []string{"dt-1", "dt-2", "dt-1"} {
		if err := s.Add(Record{ID: id, Name: id}); err != nil {
			t.Fatal(err)
		}
	}
	recs, _ := s.List()
	if len(recs) != 2 || recs[0].ID != "dt-1" || recs[1].ID != "dt-2" || !s.Has("dt-2") || s.Has("dt-9") {
		t.Fatalf("records = %+v", recs)
	}
}
