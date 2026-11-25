package client

import (
	"testing"

	"github.com/hearth-project/hearth-cli/internal/api"
)

func TestResponseLimit(t *testing.T) {
	chunk := int64(api.MaxChunkBytes + (1 << 20))
	bundle := int64(api.MaxLibraryBytes + (1 << 20))
	cases := []struct {
		method, path string
		want         int64
	}{
		{"GET", "/v1/teams/infer/packages/skill/ppt-master/1", bundle},
		{"GET", "/v1/teams/infer/packages/agent/big/latest", bundle},
		{"GET", "/v1/teams/infer/packages", chunk},
		{"GET", "/v1/library", bundle},
		{"GET", "/v1/sessions/x/content", 4 << 30},
		{"PUT", "/v1/teams/infer/packages/skill/ppt-master", chunk},
		{"GET", "/v1/devices", chunk},
	}
	for _, c := range cases {
		if got := responseLimit(c.method, c.path); got != c.want {
			t.Errorf("%s %s: limit %d, want %d", c.method, c.path, got, c.want)
		}
	}
}
