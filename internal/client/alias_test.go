package client

import "testing"

func TestMachineAlias(t *testing.T) {
	for _, c := range []struct {
		name  string
		facts map[string]any
		want  string
	}{
		{"johor-edge-gpu-e05-13.bec-host", map[string]any{"os": "linux", "arch": "aarch64", "gpu": "cf-ng-gbzz2-o-l"}, "gb-johor-edge-gpu-e05-13"},
		{"dc1-inf-sci-k8s-bzz2-0399.dc1.example.com", map[string]any{"os": "linux", "arch": "x86_64", "gpu": "cf-ng-bzz2-o"}, "b-dc1-inf-sci-k8s-bzz2-gpu-0399"},
		{"10.0.0.7", map[string]any{"os": "linux", "arch": "x86_64", "gpu": "a30"}, "a30-gpu-10-0-0-7"},
		{"node-h800-12", map[string]any{"os": "linux", "gpu": "h800"}, "node-h800-gpu-12"},
		{"MacBook-Pro-2.local", map[string]any{"os": "darwin", "arch": "aarch64", "gpu": "apple"}, "macbook-pro-2"},
		{"build-box", map[string]any{"os": "linux", "arch": "x86_64"}, "cpu-build-box"},
		{"pi-3", map[string]any{"os": "linux", "arch": "aarch64"}, "pi-arm-3"},
		{"gb200-node-1", map[string]any{"os": "linux", "gpu": "gb200"}, "gb200-node-gpu-1"},
	} {
		if got := MachineAlias(c.name, c.facts); got != c.want {
			t.Errorf("%s: %s, want %s", c.name, got, c.want)
		}
	}
	a := MachineAliases(t.TempDir(), []string{"aaaaaaaa1", "bbbbbbbb2"}, []string{"x.a", "x.b"}, []map[string]any{{}, {}})
	if a[0] == a[1] {
		t.Fatalf("not unique: %v", a)
	}
}
