package server

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/crypto"
)

// The console's crypto (panel/seal.js) must match internal/crypto: the
// install command it makes is opened by the Go client. Runs with Node when
// it is installed.
func TestPanelCryptoMatchesClient(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	key, vk, secret := crypto.RandomBytes(32), crypto.RandomBytes(32), crypto.RandomBytes(32)
	wrapped, err := crypto.WrapWithKEK(vk, key)
	if err != nil {
		t.Fatal(err)
	}
	in, _ := json.Marshal(map[string][]byte{"key": key, "wrapped": wrapped, "secret": secret, "long": bytes.Repeat([]byte("x"), 200)})
	script := `
const HC = require(process.argv[2]);
const d = JSON.parse(require("fs").readFileSync(0, "utf8"));
const b = (s) => HC.b64dec(s), e = HC.b64enc;
const vk = HC.open(b(d.key), b(d.wrapped), HC.utf8("hearth/vault-key/kek/v1"));
let tampered = false;
try { const w = b(d.wrapped); w[30] ^= 1; HC.open(b(d.key), w, HC.utf8("hearth/vault-key/kek/v1")); } catch (x) { tampered = true; }
const s = b(d.secret);
console.log(JSON.stringify({
  vk: e(vk), tampered,
  install: e(HC.seal(HC.hkdf(s, "hearth-install-key"), vk, HC.utf8("hearth/vault-key/install/v1"))),
  long: e(HC.seal(HC.hkdf(s, "hearth-install-key"), b(d.long), HC.utf8(""))),
  authHash: e(HC.sha256(HC.hkdf(s, "hearth-install-auth"))),
  hmacLong: e(HC.hmac(b(d.long), HC.utf8("m"))),
}));`
	f := filepath.Join(t.TempDir(), "check.js")
	if err := os.WriteFile(f, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	abs, _ := filepath.Abs("panel/seal.js")
	cmd := exec.Command(node, f, abs)
	cmd.Stdin = bytes.NewReader(in)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("node: %v %s", err, out)
	}
	var r struct {
		VK, Install, Long, AuthHash, HmacLong []byte
		Tampered                              bool
	}
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !bytes.Equal(r.VK, vk) || !r.Tampered {
		t.Fatalf("open: vk match %v, tamper detected %v", bytes.Equal(r.VK, vk), r.Tampered)
	}
	got, err := crypto.UnwrapWithInstall(r.Install, crypto.InstallKey(secret))
	if err != nil || !bytes.Equal(got, vk) {
		t.Fatalf("Go cannot open the console's install wrap: %v", err)
	}
	long, err := crypto.Open(crypto.InstallKey(secret), r.Long, nil)
	if err != nil || !bytes.Equal(long, bytes.Repeat([]byte("x"), 200)) {
		t.Fatalf("multi-block seal: %v", err)
	}
	if !bytes.Equal(r.AuthHash, installAuthHash(crypto.InstallAuth(secret))) {
		t.Fatal("auth hash differs")
	}
	if base64.StdEncoding.EncodeToString(r.HmacLong) != base64.StdEncoding.EncodeToString(hmacSHA256(bytes.Repeat([]byte("x"), 200), []byte("m"))) {
		t.Fatal("hmac with a long key differs")
	}
}

func hmacSHA256(key, msg []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(msg)
	return m.Sum(nil)
}
