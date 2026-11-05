package datatool

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const metaJSON = `{"code":0,"msg":"ok","data":{"storages":["a@one","b@two"],
"validPairs":[{"src":"a@one","dst":"b@two"},{"src":"b@two","dst":"a@one"}],
"pathHints":{"a@one":"/data/<user>/<dir>"},
"defaultOptions":{"transfers":8,"checkers":16,"dryRun":false},
"limits":{"NameMaxLen":128,"PathMaxLen":1024,"TransfersMax":32,"CheckersMax":64},
"destinationEmptyHint":"destination must be empty"}}`

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "", "tok", 5*time.Second, false)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestMetaAndHeaders(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != DefaultPrefix+"/meta" || r.Header.Get("X-Datatool-Token") != "tok" {
			t.Errorf("got %s with token %q", r.URL.Path, r.Header.Get("X-Datatool-Token"))
		}
		w.Write([]byte(metaJSON))
	})
	m, err := c.Meta(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(m.ValidPairs) != 2 || m.Limits.TransfersMax != 32 || m.DefaultOptions.Checkers != 16 {
		t.Fatalf("meta = %+v", m)
	}
}

func TestNoTokenNoHeader(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.Header["X-Datatool-Token"]; ok {
			t.Error("token header sent without a token")
		}
		w.Write([]byte(metaJSON))
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "/api/", "", 0, false)
	if c.Prefix != "/api" {
		t.Fatalf("prefix = %q", c.Prefix)
	}
}

func TestCreateTaskBothShapes(t *testing.T) {
	for name, data := range map[string]string{
		"task": `{"task":{"id":"dt-1","name":"n","status":"pending"}}`,
		"id":   `{"id":"dt-1"}`,
	} {
		var got CreateRequest
		c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
			if r.Method != http.MethodPost || r.URL.Path != DefaultPrefix+"/tasks" {
				t.Errorf("%s %s", r.Method, r.URL.Path)
			}
			json.NewDecoder(r.Body).Decode(&got)
			w.Write([]byte(`{"code":0,"msg":"ok","data":` + data + `}`))
		})
		task, err := c.CreateTask(context.Background(), CreateRequest{Name: "n",
			Src: StoragePath{"a@one", "/x"}, Dst: StoragePath{"b@two", "/y"}, Options: Options{Transfers: 4},
			Secrets: &Secrets{AFSUGI: "u,p"}})
		if err != nil || task.ID != "dt-1" {
			t.Fatalf("%s: %v %+v", name, err, task)
		}
		if got.Options.Transfers != 4 || got.Secrets == nil || got.Secrets.AFSUGI != "u,p" {
			t.Fatalf("%s: the request lost fields: %+v", name, got)
		}
	}
}

func TestListAndGet(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case DefaultPrefix + "/tasks":
			q := r.URL.Query()
			if q.Get("status") != "running" || q.Get("page") != "1" || q.Get("pageSize") != "50" {
				t.Errorf("query = %v", q)
			}
			w.Write([]byte(`{"code":0,"data":{"items":[{"id":"dt-2","status":"running",
"createdAt":"2026-11-05T15:45:40+08:00","startedAt":null,
"progress":{"bytes":10,"totalBytes":100,"speedBps":2.5,"etaSeconds":36,"updatedAt":"0001-01-01T00:00:00Z"}}]}}`))
		case DefaultPrefix + "/tasks/dt-2":
			w.Write([]byte(`{"code":0,"data":{"id":"dt-2","status":"succeeded"}}`))
		default:
			t.Errorf("unexpected %s", r.URL.Path)
		}
	})
	items, err := c.ListTasks(context.Background(), TaskQuery{Status: "running"})
	if err != nil || len(items) != 1 || items[0].Progress.TotalBytes != 100 || items[0].StartedAt != nil {
		t.Fatalf("%v %+v", err, items)
	}
	task, err := c.GetTask(context.Background(), "dt-2")
	if err != nil || !Finished(task.Status) {
		t.Fatalf("%v %+v", err, task)
	}
}

func TestCancelAndIDs(t *testing.T) {
	var path string
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.Method + " " + r.URL.Path
		w.Write([]byte(`{"code":0,"msg":"ok","data":null}`))
	})
	if err := c.CancelTask(context.Background(), "dt-3"); err != nil || path != "POST "+DefaultPrefix+"/tasks/dt-3/cancel" {
		t.Fatalf("%v %q", err, path)
	}
	path = ""
	for _, bad := range []string{"", "../x", "a/b", "a b", "dt-1?x=1", strings.Repeat("a", 200)} {
		if err := c.CancelTask(context.Background(), bad); err == nil {
			t.Errorf("id %q accepted", bad)
		}
		if _, err := c.GetTask(context.Background(), bad); err == nil {
			t.Errorf("id %q accepted by GetTask", bad)
		}
	}
	if path != "" {
		t.Fatalf("an invalid id reached the service: %s", path)
	}
}

func TestErrors(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case DefaultPrefix + "/meta":
			w.Write([]byte(`{"code":1001,"msg":"destination is not empty"}`)) // HTTP 200, code != 0
		case DefaultPrefix + "/tasks":
			http.Error(w, "<html>bad gateway</html>", http.StatusBadGateway)
		}
	})
	var e *Error
	if _, err := c.Meta(context.Background()); !errors.As(err, &e) || e.Code != 1001 || !strings.Contains(err.Error(), "not empty") {
		t.Fatalf("code error: %v", err)
	}
	if _, err := c.ListTasks(context.Background(), TaskQuery{}); !errors.As(err, &e) || e.HTTPStatus != 502 {
		t.Fatalf("http error: %v", err)
	}
	if _, err := New("datatool.local", "", "", 0, false); err == nil {
		t.Fatal("a url without a scheme was accepted")
	}
}

func TestURLWithCredentialsIsRefused(t *testing.T) {
	_, err := New("http://user:secret@127.0.0.1:1", "", "", time.Second, false)
	if err == nil || strings.Contains(err.Error(), "secret@") {
		t.Fatalf("expected a refusal that does not echo the password: %v", err)
	}
}

func TestUnreachable(t *testing.T) {
	c, _ := New("http://127.0.0.1:1", "", "", time.Second, false)
	if _, err := c.Meta(context.Background()); err == nil || !strings.Contains(err.Error(), "transfer service") {
		t.Fatalf("got %v", err)
	}
}
