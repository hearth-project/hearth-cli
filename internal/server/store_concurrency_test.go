package server

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/server/servertest"
)

// Concurrent writers must keep the same guarantees on every backend
// (SQLite serialises writers; PostgreSQL relies on row locks and CAS).
// Run with HEARTH_TEST_POSTGRES / HEARTH_TEST_S3 to cover them.
func TestConcurrentWriters(t *testing.T) {
	db, blobs := servertest.Backends(t)
	st, err := openStore(t.TempDir(), db, blobs)
	if err != nil {
		t.Fatal(err)
	}
	defer st.close()
	u := userRow{Email: "a@x", KDFParams: "{}", KDFSalt: []byte{1}, AuthSalt: []byte{1}, AuthHash: []byte{1}, EncVaultKey: []byte{1}, EncVaultKeyRecovery: []byte{1}, RecoverySalt: []byte{}, RecoveryHash: []byte{}}
	const n = 16
	run := func(f func(i int) error) []error {
		errs := make([]error, n)
		var wg sync.WaitGroup
		for i := range n {
			wg.Add(1)
			go func() { defer wg.Done(); errs[i] = f(i) }()
		}
		wg.Wait()
		return errs
	}
	count := func(errs []error, target error) (ok, match int) {
		for _, e := range errs {
			switch {
			case e == nil:
				ok++
			case target != nil && errors.Is(e, target):
				match++
			default:
				t.Errorf("unexpected error: %v", e)
			}
		}
		return
	}

	// One invite registers exactly one account.
	if err := st.createInvite([]byte("inv"), 1, 1<<40); err != nil {
		t.Fatal(err)
	}
	errs := run(func(i int) error {
		v := u
		v.Email = fmt.Sprintf("u%d@x", i)
		_, err := st.createUser(v, []byte("inv"), 2)
		return err
	})
	if ok, bad := count(errs, errInvite); ok != 1 || bad != n-1 {
		t.Fatalf("invite used %d times", ok)
	}
	uid, err := st.createUser(u, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.createDevice(uid, "d1", "d1", 1); err != nil {
		t.Fatal(err)
	}

	// Racing appends at the same offset: one commit, the rest are replays.
	if err := st.upsertSession(uid, "s1", []byte("m"), "d1", 1); err != nil {
		t.Fatal(err)
	}
	errs = run(func(int) error {
		_, err := st.appendChunk(uid, "s1", "d1", 1, 0, 10, []byte("0123456789"), 3)
		return err
	})
	count(errs, errConflict)
	if r, _ := st.getSession(uid, "s1"); r.Size != 10 {
		t.Fatalf("size after racing appends = %d", r.Size)
	}
	// Appends from many goroutines (distinct lengths, so none is mistaken
	// for a replay): each commits exactly once, in some order.
	errs = run(func(i int) error {
		for {
			r, _ := st.getSession(uid, "s1")
			_, err := st.appendChunk(uid, "s1", "d1", 1, r.Size, int64(i+1), make([]byte, i+1), 3)
			if errors.Is(err, errConflict) {
				continue
			}
			return err
		}
	})
	count(errs, nil)
	if r, _ := st.getSession(uid, "s1"); r.Size != 10+n*(n+1)/2 {
		t.Fatalf("size = %d, want %d", r.Size, 10+n*(n+1)/2)
	}
	if cs, _ := st.listChunks(uid, "s1", 1); len(cs) != n+1 {
		t.Fatalf("%d chunks", len(cs))
	}

	// First library upload: one wins, the others see a conflict.
	errs = run(func(i int) error {
		_, err := st.putLibrary(uid, "d1", 0, []byte(fmt.Sprint(i)), fmt.Sprint(i), 4)
		return err
	})
	if ok, bad := count(errs, errConflict); ok != 1 || bad != n-1 {
		t.Fatalf("library: %d winners", ok)
	}
	lib, err := st.getLibrary(uid)
	if err != nil || lib.Version != 1 {
		t.Fatalf("library %+v %v", lib, err)
	}
	if data, err := st.readObject(lib.Object); err != nil || string(data) != lib.SHA256 {
		t.Fatalf("winner's bundle overwritten: %q vs %q (%v)", data, lib.SHA256, err)
	}

	// Concurrent publishes of one package get distinct versions.
	if err := st.createTeam(uid, "t", 1); err != nil {
		t.Fatal(err)
	}
	tm, err := st.membership(uid, "t", "member")
	if err != nil {
		t.Fatal(err)
	}
	vers := make([]int64, n)
	errs = run(func(i int) error {
		v, _, err := st.publish(tm.ID, uid, "skill", "x", "d", fmt.Sprint(i), []byte(fmt.Sprint(i)), "", 5)
		vers[i] = v
		return err
	})
	count(errs, nil)
	seen := map[int64]bool{}
	for _, v := range vers {
		seen[v] = true
	}
	if len(seen) != n {
		t.Fatalf("versions %v", vers)
	}
	if rev, _ := st.teamRev(tm.ID); rev != n {
		t.Fatalf("team rev %d", rev)
	}
}
