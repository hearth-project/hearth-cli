package recipe

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func tarGz(t *testing.T, files map[string]string) []byte {
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	zw.Write(raw.Bytes())
	zw.Close()
	return gz.Bytes()
}

func sum(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func TestToolsAndServices(t *testing.T) {
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("tar not installed")
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	// A "node" distribution whose npm installs a fake CLI into its bin/.
	node := tarGz(t, map[string]string{
		"node-v24/bin/node": "#!/bin/sh\necho v24.4.1\n",
		"node-v24/bin/npm":  "#!/bin/sh\n# npm install -g <pkg>@<ver>\nv=${3##*@}\nprintf '#!/bin/sh\\necho codex-cli %s\\n' \"$v\" > \"$(dirname \"$0\")/codex\"\nchmod +x \"$(dirname \"$0\")/codex\"\n",
	})
	mixin := tarGz(t, map[string]string{"codex-mixin-0.7.3/codex-mixin": "#!/bin/sh\necho codex-mixin 0.7.3\n"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/node-"+archNode()+".tar.gz"):
			w.Write(node)
		case strings.HasSuffix(r.URL.Path, "/mixin-"+Arch()+".tar.gz"):
			w.Write(mixin)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	src := `
name = "codex-stack"

[[step]]
type = "tool"
name = "codex-mixin"
depends_on = ["codex"]
version = "0.7.3"
url = "` + srv.URL + `/mixin-${ARCH}.tar.gz"
sha256 = { ` + Arch() + ` = "` + sum(mixin) + `" }
bin = "*/codex-mixin"
bin_dest = "~/.local/bin/codex-mixin"

[[step]]
type = "tool"
name = "codex"
depends_on = ["node24"]
npm = "@openai/codex@0.163.0"
node = "node24"
wrapper = "~/bin/codex"

[[step]]
type = "tool"
name = "node24"
version = "24.4.1"
url = "` + srv.URL + `/node-${ARCH_NODE}.tar.gz"
sha256 = { ` + Arch() + ` = "` + sum(node) + `" }
install_dir = "~/opt/node24"
strip = 1
check = "~/opt/node24/bin/node --version"

[[step]]
type = "service"
name = "gateway"
start = "touch ~/gateway-up"
health = "test -f ~/gateway-up"
`
	r, err := Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	h := &fakeHost{secrets: map[string][]byte{}, lib: map[string][]byte{}}
	opt := Options{Mode: Apply, StateDir: filepath.Join(home, ".hearth"), Approve: true}
	res := mustRun(t, []*Recipe{r}, h, opt)
	var order []string
	o := map[string]string{}
	for _, x := range res {
		order = append(order, x.Step)
		o[x.Step] = x.Op + " " + x.Detail
	}
	if strings.Join(order[:3], ",") != "3:tool node24,2:tool codex,1:tool codex-mixin" {
		t.Fatalf("dependency order: %v", order)
	}
	for _, k := range []string{"3:tool node24", "2:tool codex", "1:tool codex-mixin", "4:service gateway"} {
		if !strings.HasPrefix(o[k], "applied") {
			t.Fatalf("%s: %s", k, o[k])
		}
	}
	out, err := exec.Command(filepath.Join(home, "bin/codex"), "--version").Output()
	if err != nil || strings.TrimSpace(string(out)) != "codex-cli 0.163.0" {
		t.Fatalf("wrapper: %q %v", out, err)
	}
	for _, x := range mustRun(t, []*Recipe{r}, h, Options{Mode: Apply, StateDir: opt.StateDir}) {
		if x.Op != "ok" {
			t.Fatalf("second run not idempotent: %+v", x)
		}
	}

	// Wrong checksum: refuse to install, and dependants are skipped.
	os.RemoveAll(filepath.Join(home, "opt"))
	os.Remove(filepath.Join(home, "bin/codex"))
	r.Steps[2].SHA256[Arch()] = strings.Repeat("0", 64)
	o = map[string]string{}
	for _, x := range mustRun(t, []*Recipe{r}, h, opt) {
		o[x.Step] = x.Op + " " + x.Detail
	}
	if !strings.Contains(o["3:tool node24"], "sha256 mismatch") || !strings.HasPrefix(o["2:tool codex"], "skipped dependency node24") {
		t.Fatalf("checksum failure: %v", o)
	}
	if _, err := Parse([]byte("name='x'\n[[step]]\ntype='tool'\nname='a'\nurl='https://x/a'\nbin_dest='~/a'\n")); err == nil {
		t.Fatal("download without sha256 accepted")
	}
}
