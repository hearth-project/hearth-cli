package search

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestTokenize(t *testing.T) {
	got := tokenize("压测吞吐 Benchmark-warnings, GPU显存")
	want := []string{"压测", "测吞", "吞吐", "benchmark", "warn", "gpu", "显存"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q want %q", got, want)
	}
}

func skillDir(t *testing.T, root, name, md string) {
	t.Helper()
	d := filepath.Join(root, name)
	os.MkdirAll(d, 0o755)
	if err := os.WriteFile(filepath.Join(d, "SKILL.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSearchRanksRelevantSkill(t *testing.T) {
	root := t.TempDir()
	skillDir(t, root, "benchmark", "---\nname: benchmark\ndescription: 管理 SGLang 性能压测、数据集和吞吐时延对比\n---\nRun load tests.\n")
	skillDir(t, root, "debug-cuda-crash", "---\ndescription: Debug CUDA crashes using kernel API logging\n---\nillegal memory access\n")
	skillDir(t, root, "drawio-skill", "---\ndescription: |\n  Create draw.io diagrams:\n  architecture, UML\n---\n")
	skillDir(t, root, ".system", "---\ndescription: hidden\n---\n")
	skillDir(t, root, "no-front-matter", "just text about kubernetes\n")
	skills, err := LoadDir(root, "library")
	if err != nil || len(skills) != 4 {
		t.Fatalf("load: %d %v", len(skills), err)
	}
	ix := Build(skills)
	cases := map[string]string{
		"帮我压测一下吞吐":                  "benchmark",
		"CUDA crash illegal memory": "debug-cuda-crash",
		"画架构图 architecture diagram": "drawio-skill",
		"kubernetes":                "no-front-matter",
		"use drawio-skill please":   "drawio-skill",
	}
	for q, want := range cases {
		res := ix.Search(q, 3)
		if len(res) == 0 || res[0].Name != want {
			t.Errorf("%q: got %+v, want %s", q, res, want)
		}
	}
	if res := ix.Search("完全无关的查询 zzzz", 3); len(res) != 0 {
		t.Errorf("unrelated query matched %+v", res)
	}
	if s, ok := ix.Get("drawio-skill"); !ok || s.Description == "" {
		t.Errorf("block-scalar description not parsed: %+v", s)
	}
}

func benchIndex(n int) *Index {
	words := []string{"benchmark", "profile", "nsys", "cuda", "kernel", "deploy", "压测", "吞吐", "显存", "调度", "日志", "icafe", "docker", "trace", "memory", "cache"}
	skills := make([]Skill, n)
	for i := range skills {
		w1, w2, w3 := words[i%len(words)], words[(i*7)%len(words)], words[(i*13)%len(words)]
		skills[i] = NewSkill(fmt.Sprintf("skill-%d-%s", i, w1), w1+" "+w2+" 相关的工具与流程", strings.Repeat(w3+" steps and notes ", 40), "bench")
	}
	return Build(skills)
}

func BenchmarkSearch5000(b *testing.B) {
	ix := benchIndex(5000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ix.Search("帮我压测一下吞吐 benchmark throughput", 5)
	}
}

func BenchmarkBuild5000(b *testing.B) {
	for i := 0; i < b.N; i++ {
		benchIndex(5000)
	}
}
