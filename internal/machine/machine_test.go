package machine

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestNormalizeGPU(t *testing.T) {
	for in, want := range map[string]string{
		"NVIDIA H800": "h800", "NVIDIA H100 80GB HBM3": "h100", "NVIDIA GB200": "gb200", "NVIDIA B200": "b200",
		"NVIDIA A30": "a30", "NVIDIA A100-SXM4-80GB": "a100", "NVIDIA L40S": "l40s", "Tesla T4": "t4",
		"NVIDIA GeForce RTX 4090": "rtx4090", "NVIDIA GH200 480GB": "gh200",
	} {
		if got := NormalizeGPU(in); got != want {
			t.Errorf("%s: %q want %q", in, got, want)
		}
	}
}

func TestDetect(t *testing.T) {
	old := Detector
	defer func() { Detector = old }()
	Detector = func(string, ...string) ([]byte, error) { return []byte("NVIDIA H800\nNVIDIA H800\n"), nil }
	t.Setenv("HEARTH_LABELS", "pdc, cluster-a")
	f := Detect()
	if f.GPU != "h800" || f.GPUCount != 2 || len(f.Labels) != 2 || f.Labels[1] != "cluster-a" {
		t.Fatalf("%+v", f)
	}
	Detector = func(string, ...string) ([]byte, error) { return nil, errors.New("no nvidia-smi") }
	t.Setenv("HEARTH_MACHINE", `{"os":"darwin","arch":"arm64"}`)
	if f := Detect(); f.OS != "darwin" || f.Arch != "aarch64" || f.GPU != "" {
		t.Fatalf("override %+v", f)
	}
}

func TestMatching(t *testing.T) {
	h800 := Facts{OS: "linux", Arch: "x86_64", GPU: "h800", Hostname: "gpu-17", Labels: []string{"pdc"}}
	gb := Facts{OS: "linux", Arch: "aarch64", GPU: "gb200"}
	mac := Facts{OS: "darwin", Arch: "aarch64", GPU: "apple"}
	cpu := Facts{OS: "linux", Arch: "x86_64"}
	cases := []struct {
		m    map[string]string
		want [4]bool // h800, gb, mac, cpu
	}{
		{map[string]string{"gpu": "h800,h100"}, [4]bool{true, false, false, false}},
		{map[string]string{"arch": "arm64"}, [4]bool{false, true, true, false}},
		{map[string]string{"os": "mac"}, [4]bool{false, false, true, false}},
		{map[string]string{"os": "linux", "gpu": "!none"}, [4]bool{true, true, false, false}},
		{map[string]string{"gpu": "none"}, [4]bool{false, false, false, true}},
		{map[string]string{"gpu": "gb*,b*"}, [4]bool{false, true, false, false}},
		{map[string]string{"hostname": "gpu-*", "label": "pdc"}, [4]bool{true, false, false, false}},
		{map[string]string{"os": "!darwin"}, [4]bool{true, true, false, true}},
		{nil, [4]bool{}},
	}
	for _, c := range cases {
		for i, f := range []Facts{h800, gb, mac, cpu} {
			if got := f.Matches(Profile{Name: "x", Match: c.m}); got != c.want[i] {
				t.Errorf("%v on %+v: %v", c.m, f, got)
			}
		}
	}
}

func TestFileRoundTripAndActive(t *testing.T) {
	p := filepath.Join(t.TempDir(), "machines.toml")
	f := &File{}
	f.Set(Profile{Name: "h800", Match: map[string]string{"gpu": "h800"}, Workspace: "/mnt/ssd/$USER"})
	f.Set(Profile{Name: "mac", Match: map[string]string{"os": "darwin"}, Workspace: "~/work"})
	f.Set(Profile{Name: "special"})
	if err := f.Save(p); err != nil {
		t.Fatal(err)
	}
	g, err := Load(p)
	if err != nil || len(g.Profiles) != 3 || g.Profiles[0].Workspace != "/mnt/ssd/$USER" {
		t.Fatalf("%+v %v", g, err)
	}
	facts := Facts{OS: "linux", GPU: "h800"}
	if n := Names(g.Active(facts, nil)); len(n) != 1 || n[0] != "h800" {
		t.Fatal(n)
	}
	if n := Names(g.Active(facts, []string{"special", "h800"})); len(n) != 2 || n[0] != "special" {
		t.Fatal(n)
	}
	bad := &File{Profiles: []Profile{{Name: "x", Match: map[string]string{"gpus": "h800"}}}}
	if bad.Validate() == nil {
		t.Fatal("unknown key accepted")
	}
	if s := Suggest(Facts{OS: "linux", Arch: "x86_64", GPU: "h800", GPUCount: 8, GPUName: "NVIDIA H800"}); s.Name != "h800" || s.Match["gpu"] != "h800" {
		t.Fatalf("%+v", s)
	}
	if s := Suggest(Facts{OS: "darwin", Arch: "aarch64", GPU: "apple"}); s.Name != "mac" {
		t.Fatalf("%+v", s)
	}
	if s := Suggest(Facts{OS: "linux", Arch: "aarch64"}); s.Name != "linux-aarch64" || !(Facts{OS: "linux", Arch: "aarch64"}).Matches(s) {
		t.Fatalf("%+v", s)
	}
}
