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

	"github.com/hearth-project/hearth-cli/internal/server/servertest"
)

func TestPortalTeamMemberSearchAndAdd(t *testing.T) {
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
	newUser("new-user@example.com")
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

	call := func(method, path, body string, uid int64, email string, handler func(http.ResponseWriter, *http.Request, userCtx)) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.SetPathValue("team", "infer")
		w := httptest.NewRecorder()
		handler(w, r, userCtx{uid: uid, email: email})
		return w
	}

	w := call("GET", "/v1/me/teams/infer/members/search?q=new-user", "", owner, "owner@example.com", s.apiMyTeamMemberSearch)
	if w.Code != http.StatusOK {
		t.Fatalf("owner search status %d: %s", w.Code, w.Body.String())
	}
	var found []memberSearchRow
	if err := json.Unmarshal(w.Body.Bytes(), &found); err != nil {
		t.Fatal(err)
	}
	if len(found) != 1 || found[0].Email != "new-user@example.com" {
		t.Fatalf("owner search result: %+v", found)
	}

	w = call("POST", "/v1/me/teams/infer/members", `{"email":"new-user@example.com","role":"member"}`, owner, "owner@example.com", s.apiMyTeamMemberAdd)
	if w.Code != http.StatusOK {
		t.Fatalf("owner add status %d: %s", w.Code, w.Body.String())
	}
	if _, err := st.membership(newUserID(t, st, "new-user@example.com"), "infer", "member"); err != nil {
		t.Fatalf("new user was not added: %v", err)
	}

	w = call("POST", "/v1/me/teams/infer/members", `{"email":"new-user@example.com","role":"owner"}`, maintainer, "maintainer@example.com", s.apiMyTeamMemberAdd)
	if w.Code != http.StatusForbidden {
		t.Fatalf("maintainer owner escalation status %d: %s", w.Code, w.Body.String())
	}
	w = call("POST", "/v1/me/teams/infer/members", `{"email":"not-registered@example.com"}`, owner, "owner@example.com", s.apiMyTeamMemberAdd)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "no registered user") {
		t.Fatalf("unregistered add status %d: %s", w.Code, w.Body.String())
	}
	w = call("GET", "/v1/me/teams/infer/members/search?q=owner", "", member, "member@example.com", s.apiMyTeamMemberSearch)
	if w.Code != http.StatusForbidden {
		t.Fatalf("member search status %d: %s", w.Code, w.Body.String())
	}
	w = call("POST", "/v1/me/teams/infer/members", `{"email":"new-user@example.com"}`, member, "member@example.com", s.apiMyTeamMemberAdd)
	if w.Code != http.StatusForbidden {
		t.Fatalf("member add status %d: %s", w.Code, w.Body.String())
	}
}

func newUserID(t *testing.T, st *store, email string) int64 {
	t.Helper()
	u, err := st.userByEmail(email)
	if err != nil {
		t.Fatal(err)
	}
	return u.ID
}
