package client

import (
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/hearth-project/hearth-cli/internal/server"
)

// Admin operations and quotas, through a second Server on the same storage
// (what `hearth server admin` does while the server runs).
func TestAdminAndQuota(t *testing.T) {
	en := newEnv(t)
	a := en.machine("a")
	a.appendLines(t, 20, "before-quota")
	a.push(t)
	if err := a.SetSecret(Secret{Name: "TOK", Value: []byte("v")}); err != nil {
		t.Fatal(err)
	}
	cfg := en.cfg
	cfg.Logger, cfg.Now = log.New(io.Discard, "", 0), en.clock.now
	admin, err := server.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()

	u, err := admin.User("dev@example.com")
	if err != nil || u.Sessions != 1 || u.SessionBytes == 0 || u.Secrets != 1 || u.Devices != 1 || u.QuotaBytes != 0 {
		t.Fatalf("user %+v %v", u, err)
	}
	usage, err := a.API.Usage()
	if err != nil || usage.Used != u.Used() || usage.Quota != 0 {
		t.Fatalf("self usage %+v %v (admin says %d)", usage, err, u.Used())
	}
	if st, err := admin.Stats(); err != nil || st.Users != 1 || st.Sessions != 1 {
		t.Fatalf("stats %+v %v", st, err)
	}

	// Quota: just above current use; the next big append is refused, the
	// client pauses instead of retrying every pass.
	if err := admin.SetUserQuota("dev@example.com", u.Used()+100); err != nil {
		t.Fatal(err)
	}
	en.clock.add(time.Minute) // past the running server's quota cache refresh
	a.appendLines(t, 50, "over-quota")
	if st, _ := a.PushOnce(); st.Skipped != 1 || st.Bytes != 0 || a.quotaUntil.IsZero() {
		t.Fatalf("over quota push: %+v", st)
	}
	before := a.quotaUntil
	if st, _ := a.PushOnce(); st.Skipped != 1 || a.quotaUntil != before {
		t.Fatalf("second push should be paused locally without asking the server: %+v", st)
	}
	if usage, _ := a.API.Usage(); usage.Quota != u.Used()+100 {
		t.Fatalf("quota not reported: %+v", usage)
	}
	// Raising it lets the upload through at once (after the client backoff).
	admin.SetUserQuota("dev@example.com", 0)
	en.clock.add(2 * time.Second)
	a.quotaUntil = time.Time{}
	if st, err := a.PushOnce(); err != nil || st.Bytes == 0 {
		t.Fatalf("after raising quota: %+v %v", st, err)
	}

	// Disable: tokens stop working (after the cache revalidates), login is refused.
	if err := admin.SetUserStatus("dev@example.com", false); err != nil {
		t.Fatal(err)
	}
	en.clock.add(time.Minute)
	if _, err := a.API.Sessions(); StatusOf(err) != 401 {
		t.Fatalf("disabled account still served: %v", err)
	}
	if _, err := Login(t.TempDir(), en.url, "dev@example.com", []byte(pw), "x"); StatusOf(err) != 403 {
		t.Fatalf("disabled login: %v", err)
	}
	admin.SetUserStatus("dev@example.com", true)
	en.clock.add(time.Minute)
	if _, err := a.API.Sessions(); err != nil {
		t.Fatalf("re-enabled: %v", err)
	}
	if n, err := admin.RevokeUserDevices("dev@example.com"); err != nil || n != 1 {
		t.Fatalf("revoke %d %v", n, err)
	}
	en.clock.add(time.Minute)
	if _, err := a.API.Sessions(); StatusOf(err) != 401 {
		t.Fatalf("revoked device still served: %v", err)
	}

	// Delete: refused while the only owner of a team, then removes all data.
	b := en.user("bob@example.com", "bob")
	if err := b.API.CreateTeam("ml"); err != nil {
		t.Fatal(err)
	}
	b.appendLines(t, 5, "bob")
	b.push(t)
	if err := admin.DeleteUser("bob@example.com"); err == nil || !strings.Contains(err.Error(), "only owner") {
		t.Fatalf("delete sole owner: %v", err)
	}
	if err := b.API.SetMember("ml", "dev@example.com", "owner"); err != nil {
		t.Fatal(err)
	}
	if err := admin.DeleteUser("bob@example.com"); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.User("bob@example.com"); err == nil {
		t.Fatal("deleted user still listed")
	}
	if us, _ := admin.Users(); len(us) != 1 {
		t.Fatalf("users %+v", us)
	}

	// Invites: list and revoke.
	code, _ := admin.CreateInvite(time.Hour)
	in, _ := admin.Invites()
	var id string
	for _, i := range in {
		if i.UsedBy == "" {
			id = i.ID
		}
	}
	if err := admin.RevokeInvite(id); err != nil {
		t.Fatal(err)
	}
	if _, err := Register(t.TempDir(), en.url, "new@example.com", []byte(pw), code); err == nil {
		t.Fatal("revoked invite accepted")
	}

	// Orphan sweep: an unreferenced old blob is removed, live ones are not.
	if !strings.Contains(admin.Storage(), "file://") {
		return // the remaining check plants a file in the local blob dir
	}
	orphan := filepath.Join(en.data, "objects", "1", "orphan")
	os.MkdirAll(filepath.Dir(orphan), 0o700)
	os.WriteFile(orphan, []byte("junk"), 0o600)
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(orphan, old, old)
	sw, err := admin.SweepOrphans(time.Hour, true)
	if err != nil || sw.Orphans != 1 || sw.Bytes != 4 {
		t.Fatalf("dry run %+v %v", sw, err)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatal("dry run deleted")
	}
	if sw, err = admin.SweepOrphans(time.Hour, false); err != nil || sw.Deleted != 1 {
		t.Fatalf("sweep %+v %v", sw, err)
	}
	if _, err := os.Stat(orphan); err == nil {
		t.Fatal("orphan kept")
	}
	// Live data survives the sweep.
	c := en.machine("c")
	if _, err := c.Restore(c.only(t), false); err != nil {
		t.Fatalf("restore after sweep: %v", err)
	}
}
