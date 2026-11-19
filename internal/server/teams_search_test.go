package server

import (
	"testing"

	"github.com/hearth-project/hearth-cli/internal/server/servertest"
)

func TestSearchRegisteredMembers(t *testing.T) {
	db, blobs := servertest.Backends(t)
	st, err := openStore(t.TempDir(), db, blobs)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()

	newUser := func(email, status string) int64 {
		t.Helper()
		uid, err := st.createUser(userRow{
			Email: email, KDFParams: "{}", KDFSalt: []byte{1}, AuthSalt: []byte{1},
			AuthHash: []byte{1}, EncVaultKey: []byte{1}, EncVaultKeyRecovery: []byte{1},
			RecoverySalt: []byte{}, RecoveryHash: []byte{}, Status: status,
		}, nil, 1)
		if err != nil {
			t.Fatal(err)
		}
		return uid
	}

	owner := newUser("owner@example.com", "active")
	newUser("zhengshifeng@example.com", "active")
	newUser("zhengshang@example.com", "active")
	newUser("zhengdisabled@example.com", "disabled")
	if err := st.createTeam(owner, "infer", 1); err != nil {
		t.Fatal(err)
	}
	team, err := st.membership(owner, "infer", "member")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.setMember(team.ID, "zhengshifeng@example.com", "member", 2); err != nil {
		t.Fatal(err)
	}

	got, err := st.searchRegisteredMembers(team.ID, "zheng")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d search results: %+v", len(got), got)
	}
	if got[0].Email != "zhengshang@example.com" || got[0].InTeam {
		t.Fatalf("unexpected first result: %+v", got[0])
	}
	if got[1].Email != "zhengshifeng@example.com" || !got[1].InTeam || got[1].Role != "member" {
		t.Fatalf("unexpected second result: %+v", got[1])
	}

	got, err = st.searchRegisteredMembers(team.ID, "zhengshifeng@example")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Email != "zhengshifeng@example.com" {
		t.Fatalf("full email prefix search: %+v", got)
	}

	got, err = st.searchRegisteredMembers(team.ID, "zhengdisabled")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("disabled account appeared in search: %+v", got)
	}

	got, err = st.searchRegisteredMembers(team.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("empty query listed accounts: %+v", got)
	}
}
