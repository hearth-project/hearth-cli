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

func TestPortalTeamPackageUsage(t *testing.T) {
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
	bob := newUser("bob@example.com")
	carol := newUser("carol@example.com")
	if err := st.createTeam(owner, "infer", 1); err != nil {
		t.Fatal(err)
	}
	team, err := st.membership(owner, "infer", "member")
	if err != nil {
		t.Fatal(err)
	}
	for _, member := range []struct {
		uid  int64
		role string
	}{
		{bob, "member"},
		{carol, "member"},
	} {
		email, _ := st.emailOf(member.uid)
		if err := st.setMember(team.ID, email, member.role, 2); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := st.publish(team.ID, owner, "skill", "bench", "", "skill-sum", []byte("skill"), "", 3); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.publish(team.ID, owner, "agent", "reviewer", "", "agent-sum", []byte("agent"), "", 3); err != nil {
		t.Fatal(err)
	}
	day := time.Now().UTC().Format(time.DateOnly)
	for _, row := range []struct {
		uid   int64
		typ   string
		name  string
		count int
	}{
		{owner, "skill", "bench", 2},
		{bob, "skill", "bench", 8},
		{carol, "skill", "bench", 4},
		{owner, "agent", "reviewer", 1},
		{carol, "agent", "reviewer", 5},
	} {
		if _, err := st.db.Exec(`INSERT INTO user_usage_daily(user_id,type,name,day,count) VALUES(?,?,?,?,?)`,
			row.uid, row.typ, row.name, day, row.count); err != nil {
			t.Fatal(err)
		}
	}

	call := func(uid int64, typ, name string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/v1/me/teams/infer/packages/"+typ+"/"+name+"/usage", nil)
		r.SetPathValue("team", "infer")
		r.SetPathValue("type", typ)
		r.SetPathValue("name", name)
		w := httptest.NewRecorder()
		s.apiTeamPackageUsage(w, r, userCtx{uid: uid})
		return w
	}
	decode := func(w *httptest.ResponseRecorder) []teamMemberUsage {
		t.Helper()
		var out []teamMemberUsage
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}

	w := call(bob, "skill", "bench")
	if w.Code != http.StatusOK {
		t.Fatalf("skill usage status %d: %s", w.Code, w.Body.String())
	}
	skill := decode(w)
	if len(skill) != 3 || skill[0].Email != "bob@example.com" || skill[0].Count != 8 ||
		skill[1].Email != "carol@example.com" || skill[1].Count != 4 ||
		skill[2].Email != "owner@example.com" || skill[2].Count != 2 {
		t.Fatalf("skill usage ranking: %+v", skill)
	}

	w = call(owner, "agent", "reviewer")
	if w.Code != http.StatusOK {
		t.Fatalf("agent usage status %d: %s", w.Code, w.Body.String())
	}
	agent := decode(w)
	if len(agent) != 2 || agent[0].Email != "carol@example.com" || agent[0].Count != 5 ||
		agent[1].Email != "owner@example.com" || agent[1].Count != 1 {
		t.Fatalf("agent usage ranking: %+v", agent)
	}

	w = call(owner, "skill", "missing")
	if w.Code != http.StatusNotFound || !strings.Contains(w.Body.String(), "not found") {
		t.Fatalf("missing package status %d: %s", w.Code, w.Body.String())
	}
}
