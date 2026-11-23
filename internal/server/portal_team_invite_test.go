package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/server/servertest"
)

func TestPortalTeamInvite(t *testing.T) {
	db, blobs := servertest.Backends(t)
	st, err := openStore(t.TempDir(), db, blobs)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	s := &Server{st: st, cfg: Config{Now: time.Now, Logger: log.New(io.Discard, "", 0)}}

	newUser := func(email string) int64 {
		t.Helper()
		uid, err := st.createUser(userRow{
			Email: email, KDFParams: "{}", KDFSalt: []byte{1}, AuthSalt: []byte{1},
			AuthHash: []byte{1}, EncVaultKey: []byte{1}, EncVaultKeyRecovery: []byte{1},
			RecoverySalt: []byte{}, RecoveryHash: []byte{},
		}, nil, 1)
		if err != nil {
			t.Fatal(err)
		}
		return uid
	}
	owner := newUser("owner@example.com")
	maintainer := newUser("maintainer@example.com")
	member := newUser("member@example.com")
	first := newUser("first@example.com")
	second := newUser("second@example.com")
	if err := st.createTeam(owner, "infer", 1); err != nil {
		t.Fatal(err)
	}
	team, err := st.membership(owner, "infer", "member")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.setMember(team.ID, "maintainer@example.com", "maintainer", 2); err != nil {
		t.Fatal(err)
	}
	if err := st.setMember(team.ID, "member@example.com", "member", 2); err != nil {
		t.Fatal(err)
	}

	invite := func(uid int64, email, body string) (*httptest.ResponseRecorder, api.Invite) {
		t.Helper()
		r := httptest.NewRequest("POST", "/v1/me/teams/infer/invite", strings.NewReader(body))
		r.SetPathValue("team", "infer")
		w := httptest.NewRecorder()
		s.apiMyTeamInvite(w, r, userCtx{uid: uid, email: email})
		var inv api.Invite
		if w.Code == http.StatusCreated {
			if err := json.Unmarshal(w.Body.Bytes(), &inv); err != nil {
				t.Fatal(err)
			}
		}
		return w, inv
	}

	// No body, or the {} older apps send: the CLI's defaults.
	for _, body := range []string{"", "{}"} {
		w, inv := invite(owner, "owner@example.com", body)
		if w.Code != http.StatusCreated || inv.Role != "maintainer" || inv.MaxUses != 0 || inv.Team != "infer" || inv.Code == "" {
			t.Fatalf("default invite (%q): %d %s", body, w.Code, w.Body.String())
		}
		if d := inv.ExpiresAt - time.Now().Unix(); d < 7*86400-60 || d > 7*86400 {
			t.Fatalf("default invite expires in %ds, want 7 days", d)
		}
	}

	// A member's code for one person.
	w, inv := invite(maintainer, "maintainer@example.com", `{"role":"member","max_uses":1}`)
	if w.Code != http.StatusCreated || inv.Role != "member" || inv.MaxUses != 1 {
		t.Fatalf("member invite: %d %s", w.Code, w.Body.String())
	}
	if o, err := st.joinTeam(first, inv.Code, time.Now().Unix()); err != nil || o.Role != "member" {
		t.Fatalf("first join: %+v %v", o, err)
	}
	if _, err := st.joinTeam(second, inv.Code, time.Now().Unix()); err == nil || !strings.Contains(err.Error(), "used up") {
		t.Fatalf("second join of a one-person code: %v", err)
	}

	// Never above the inviter's role; members cannot invite; bad bodies are refused.
	for _, c := range []struct {
		uid   int64
		email string
		body  string
		want  int
	}{
		{maintainer, "maintainer@example.com", `{"role":"owner"}`, http.StatusForbidden},
		{member, "member@example.com", `{}`, http.StatusForbidden},
		{owner, "owner@example.com", `{"role":"reader"}`, http.StatusBadRequest},
		{owner, "owner@example.com", `{"max_uses":-1}`, http.StatusBadRequest},
		{owner, "owner@example.com", `{"uses":1}`, http.StatusBadRequest},
	} {
		if w, _ := invite(c.uid, c.email, c.body); w.Code != c.want {
			t.Fatalf("%s %s: status %d, want %d: %s", c.email, c.body, w.Code, c.want, w.Body.String())
		}
	}
}
