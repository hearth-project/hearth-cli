package server

import (
	"testing"

	"github.com/hearth-project/hearth-cli/internal/server/servertest"
)

// Sessions list newest activity first; renames (metadata) do not reorder;
// limit/offset page through them with the total count.
func TestListSessionsByActivity(t *testing.T) {
	db, blobs := servertest.Backends(t)
	st, err := openStore(t.TempDir(), db, blobs)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	u := userRow{Email: "a@x", KDFParams: "{}", KDFSalt: []byte{1}, AuthSalt: []byte{1}, AuthHash: []byte{1}, EncVaultKey: []byte{1}, EncVaultKeyRecovery: []byte{1}, RecoverySalt: []byte{}, RecoveryHash: []byte{}}
	uid, err := st.createUser(u, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.createDevice(uid, "d1", "d1", 1); err != nil {
		t.Fatal(err)
	}
	for i, sid := range []string{"a", "b", "c"} {
		if err := st.upsertSession(uid, sid, []byte("m"), "d1", int64(10+i)); err != nil {
			t.Fatal(err)
		}
	}
	// "a" gets new content last; "c" is only renamed.
	if _, err := st.appendChunk(uid, "a", "d1", 1, 0, 1, []byte("x"), 50); err != nil {
		t.Fatal(err)
	}
	if err := st.upsertSession(uid, "b", []byte("m2"), "d1", 40); err != nil {
		t.Fatal(err)
	}
	if _, err := st.appendChunk(uid, "c", "d1", 1, 0, 1, []byte("x"), 30); err != nil {
		t.Fatal(err)
	}
	ids := func(rows []sessionRow) (out []string) {
		for _, r := range rows {
			out = append(out, r.ID)
		}
		return out
	}
	rows, total, err := st.listSessions(uid, 0, 0)
	if err != nil || total != 3 {
		t.Fatal(total, err)
	}
	got := ids(rows)
	if len(got) != 3 || got[0] != "a" || got[1] != "c" || got[2] != "b" {
		t.Fatalf("order %v", got)
	}
	page, total, _ := st.listSessions(uid, 2, 1)
	if total != 3 || len(page) != 2 || page[0].ID != got[1] || page[1].ID != got[2] {
		t.Fatalf("page %v of %v (total %d)", ids(page), got, total)
	}
}

// Refreshing the index (metadata) must not revive an expired (deleted)
// session or bring it back to the top of the list.
func TestMetaUpdateKeepsExpiry(t *testing.T) {
	db, blobs := servertest.Backends(t)
	st, err := openStore(t.TempDir(), db, blobs)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	u := userRow{Email: "a@x", KDFParams: "{}", KDFSalt: []byte{1}, AuthSalt: []byte{1}, AuthHash: []byte{1}, EncVaultKey: []byte{1}, EncVaultKeyRecovery: []byte{1}, RecoverySalt: []byte{}, RecoveryHash: []byte{}}
	uid, _ := st.createUser(u, nil, 2)
	st.upsertSession(uid, "a", []byte("m"), "d1", 10)
	st.upsertSession(uid, "b", []byte("m"), "d1", 20)
	if err := st.expireSession(uid, "a", 30); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.listSessions(uid, 0, 0); err != nil {
		t.Fatal(err)
	}
	if err := st.upsertSession(uid, "a", []byte("index"), "d2", 40); err != nil {
		t.Fatal(err)
	}
	r, _ := st.getSession(uid, "a")
	if r.Status != "expired" || r.ActiveAt != 10 || r.UpdatedBy != "d2" {
		t.Fatalf("after index update: %+v", r)
	}
}
