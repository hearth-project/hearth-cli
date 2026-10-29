package memory

import (
	"fmt"
	"math/rand"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIndexSearch(t *testing.T) {
	p := filepath.Join(t.TempDir(), "index.db")
	x, err := Open(p)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Unix()
	docs := []Doc{
		{ID: "s:1", Kind: "conversation", Title: "fix NCCL hang on H800", Body: "set NCCL_IB_HCA=mlx5 to stop the all-reduce hang", At: now - 86400, Cwd: "/ws/sglang"},
		{ID: "s:2", Kind: "conversation", Title: "压测吞吐下降排查", Body: "原因是 cuda graph 没开，打开后吞吐恢复", At: now},
		{ID: "f:1", Kind: "fact", Title: "commit habit", Body: "run make lint before committing", At: now - 400*86400},
		{ID: "s:3", Kind: "conversation", Title: "unrelated", Body: "write a poem", At: now},
	}
	if err := x.Apply(docs, map[string]string{"s:1": "a"}, nil); err != nil {
		t.Fatal(err)
	}
	x.Close()

	r, err := OpenReadOnly(p)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	for q, want := range map[string]string{"nccl hang": "s:1", "吞吐": "s:2", "lint commit": "f:1", "压测": "s:2"} {
		hits, err := r.Search(Query{Text: q, Limit: 3})
		if err != nil || len(hits) == 0 || hits[0].ID != want {
			t.Fatalf("%q: %+v %v", q, hits, err)
		}
	}
	if hits, _ := r.Search(Query{Text: "zzz qqq", Limit: 3}); len(hits) != 0 {
		t.Fatalf("no match expected: %+v", hits)
	}
	// Update replaces, delete removes.
	w, _ := Open(p)
	w.Apply([]Doc{{ID: "s:1", Kind: "conversation", Title: "renamed", Body: "nothing", At: now}}, nil, []string{"f:1"})
	if h, _ := w.Search(Query{Text: "nccl"}); len(h) != 0 {
		t.Fatalf("old content still found: %+v", h)
	}
	if h, _ := w.Search(Query{Text: "lint"}); len(h) != 0 {
		t.Fatalf("deleted doc found: %+v", h)
	}
	w.Close()
}

// Search stays fast at the scale of years of conversations: 20,000
// memories with a Zipf-like vocabulary, like real text.
func BenchmarkSearch(b *testing.B) {
	p := filepath.Join(b.TempDir(), "index.db")
	x, _ := Open(p)
	rng := rand.New(rand.NewSource(1))
	vocab := make([]string, 8000)
	for i := range vocab {
		vocab[i] = fmt.Sprintf("w%dx", i)
	}
	zipf := rand.NewZipf(rng, 1.1, 2, uint64(len(vocab)-1))
	var docs []Doc
	for i := 0; i < 20000; i++ {
		var words []string
		for j := 0; j < 60; j++ {
			words = append(words, vocab[zipf.Uint64()])
		}
		docs = append(docs, Doc{ID: fmt.Sprintf("s:%d", i), Kind: "conversation", Title: strings.Join(words[:6], " "),
			Body: strings.Join(words[6:], " ") + " 压测吞吐显存报错", At: time.Now().Unix() - int64(i*600)})
	}
	if err := x.Apply(docs, nil, nil); err != nil {
		b.Fatal(err)
	}
	x.Close()
	r, _ := OpenReadOnly(p)
	defer r.Close()
	queries := []string{"w12x w340x w2001x", "w1x w2x w3x", "压测 吞吐 w77x", "w5000x w6000x w7000x w100x"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Search(Query{Text: queries[i%len(queries)], Limit: 3}); err != nil {
			b.Fatal(err)
		}
	}
}
