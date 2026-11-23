package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/client"
)

func TestSetupAliasDefault(t *testing.T) {
	base := strings.Repeat("a", 80)
	ds := []api.Device{
		{ID: "online", Name: "node-1.example", Online: true},    // generated node-cpu-1
		{ID: "offline", Name: "node-cpu-1-2", Status: "active"}, // raw host name, leave it alone too
		{ID: "other", Alias: "NODE-CPU-1-3"},
		{ID: "revoked", Alias: "node-cpu-1-4", Status: "revoked"},
		{ID: "self", Alias: "node-cpu-1-4", Online: true},
		{ID: "long", Alias: base[:63]},
	}
	if got := setupAliasDefault("node-cpu-1", ds, "self", nil); got != "node-cpu-1-4" {
		t.Fatalf("default = %q", got)
	}
	if got := setupAliasDefault(base, ds, "self", nil); got != base[:61]+"-2" {
		t.Fatalf("long default = %q", got)
	}
}

// Exercise the actual prompt and HTTP writes, including devices coming online
// after the prompt and a conflict not (yet) reflected in the listing.
func TestSetupAliasEnter(t *testing.T) {
	for _, tc := range []struct {
		name                           string
		duringPrompt, firstPUTConflict bool
		want                           []string
	}{
		{"already taken", false, false, []string{"cpu-node-2"}},
		{"taken while reading", true, false, []string{"cpu-node", "cpu-node-2"}},
		{"conflict on submit", false, true, []string{"cpu-node-2", "cpu-node-3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			occupied := api.Device{ID: "other", Alias: "cpu-node", Online: true}
			var prompts []string
			gets, puts := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method + " " + r.URL.Path {
				case "GET /v1/devices":
					gets++
					ds := []api.Device{occupied}
					if tc.duringPrompt && gets == 1 {
						ds = nil
					}
					json.NewEncoder(w).Encode(ds)
				case "PUT /v1/devices/self/alias":
					puts++
					var req struct {
						Alias        string `json:"alias"`
						OnlyIfUnused bool   `json:"only_if_unused"`
					}
					if err := json.NewDecoder(r.Body).Decode(&req); err != nil || !req.OnlyIfUnused || req.Alias == occupied.Alias {
						t.Errorf("unexpected alias write: %+v %v", req, err)
					}
					if tc.firstPUTConflict && puts == 1 {
						w.WriteHeader(409)
						return
					}
					if req.Alias != tc.want[len(tc.want)-1] {
						t.Errorf("wrote alias %q", req.Alias)
					}
					w.WriteHeader(204)
				default:
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
					w.WriteHeader(500)
				}
			}))
			defer srv.Close()
			e := &client.Engine{Cfg: &client.Config{DeviceID: "self"}, API: client.NewAPI(srv.URL, "", srv.Client())}
			alias, old, err := promptSetupAlias(e, "cpu-node", func(_ []string, def string) (string, bool) {
				prompts = append(prompts, def)
				return "", len(prompts) <= len(tc.want)
			})
			if !reflect.DeepEqual(prompts, tc.want) || err != nil || alias != tc.want[len(tc.want)-1] || old != nil {
				t.Fatalf("prompts %v; result %q %+v %v", prompts, alias, old, err)
			}
		})
	}
}

// An older server disallows the extra safeguard. Never retry with an
// unguarded PUT: the base is usable as an implicit SSH name, a suffix is not.
func TestSetupAliasOldServer(t *testing.T) {
	for _, conflict := range []bool{false, true} {
		writes := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "GET" {
				var ds []api.Device
				if conflict {
					ds = append(ds, api.Device{ID: "other", Alias: "cpu-node", Online: true})
				}
				json.NewEncoder(w).Encode(ds)
				return
			}
			writes++
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(api.Error{Error: "invalid JSON body"})
		}))
		e := &client.Engine{Cfg: &client.Config{DeviceID: "self"}, API: client.NewAPI(srv.URL, "", srv.Client())}
		asked := false
		a, _, err := promptSetupAlias(e, "cpu-node", func(_ []string, _ string) (string, bool) {
			if asked {
				return "", false
			}
			asked = true
			return "", true
		})
		srv.Close()
		if writes != 1 || (conflict && (err == nil || !strings.Contains(err.Error(), "--alias"))) || (!conflict && (err != nil || a != "cpu-node")) {
			t.Fatalf("conflict %t: alias %q writes %d err %v", conflict, a, writes, err)
		}
	}
}
