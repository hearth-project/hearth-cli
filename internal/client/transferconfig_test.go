package client

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestServerTransferConfig(t *testing.T) {
	en := newEnv(t)
	m := en.machine("a")
	file := filepath.Join(en.data, "transfer.json")

	// No file: the admin set nothing.
	if c, err := m.API.TransferConfig(); err != nil || c != nil {
		t.Fatalf("no settings: %+v %v", c, err)
	}

	good := `{"url":"http://svc.example.com","token":"shared","afs_ugi_secret":"afs_ugi",
"paths":{"a@one":[{"from":"/mnt/personal","to":"/data/{user}"}]}}`
	if err := os.WriteFile(file, []byte(good), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := m.API.TransferConfig()
	if err != nil || c == nil || c.URL != "http://svc.example.com" || c.Token != "shared" ||
		c.AFSUGISecret != "afs_ugi" || len(c.Paths["a@one"]) != 1 || c.Paths["a@one"][0].To != "/data/{user}" {
		t.Fatalf("settings: %+v %v", c, err)
	}

	// Edits apply at once.
	if err := os.WriteFile(file, []byte(`{"url":"https://other.example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := m.API.TransferConfig(); err != nil || c.URL != "https://other.example.com" || c.Token != "" {
		t.Fatalf("after an edit: %+v %v", c, err)
	}

	// A mistake in the file is reported to the user, not served.
	for name, body := range map[string]string{
		"misspelt key":     `{"url":"http://svc.example.com","tokn":"x"}`,
		"not json":         `url = "http://svc.example.com"`,
		"no url":           `{"token":"x"}`,
		"credentials":      `{"url":"http://u:p@svc.example.com"}`,
		"not an http url":  `{"url":"ftp://svc.example.com"}`,
		"trailing garbage": `{"url":"http://svc.example.com"} x`,
	} {
		if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if c, err := m.API.TransferConfig(); err == nil || StatusOf(err) != http.StatusInternalServerError {
			t.Errorf("%s: served %+v, err %v", name, c, err)
		}
	}
}

func TestServerTransferConfigNeedsLogin(t *testing.T) {
	en := newEnv(t)
	if err := os.WriteFile(filepath.Join(en.data, "transfer.json"), []byte(`{"url":"http://svc.example.com","token":"shared"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(en.url + "/v1/transfer/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("without a login the token must not be served: HTTP %d", resp.StatusCode)
	}
}
