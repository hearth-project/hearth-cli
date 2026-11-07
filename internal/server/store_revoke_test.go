package server

import (
	"errors"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/server/servertest"
)

// A removed machine's token no longer authenticates, but the server can
// still tell it was removed (410) rather than expired (401).
func TestRevokeDeviceKeepsRemovedSignal(t *testing.T) {
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
	for _, d := range []string{"d1", "d2"} {
		if err := st.createDevice(uid, d, d, 1); err != nil {
			t.Fatal(err)
		}
		if err := st.createToken([]byte("tok-"+d), uid, d, 1000); err != nil {
			t.Fatal(err)
		}
	}
	if st.tokenRemoved([]byte("tok-d1"), 10) {
		t.Fatal("active machine reported removed")
	}
	if err := st.revokeDevice(uid, "d1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.lookupToken([]byte("tok-d1"), 10); !errors.Is(err, errNotFound) {
		t.Fatalf("removed machine still authenticates: %v", err)
	}
	if !st.tokenRemoved([]byte("tok-d1"), 10) {
		t.Fatal("removed machine not reported removed")
	}
	if st.tokenRemoved([]byte("tok-d1"), 2000) {
		t.Fatal("expired token reported removed")
	}
	if _, _, err := st.lookupToken([]byte("tok-d2"), 10); err != nil {
		t.Fatalf("other machine lost access: %v", err)
	}
	if st.tokenRemoved([]byte("unknown"), 10) {
		t.Fatal("unknown token reported removed")
	}
}
