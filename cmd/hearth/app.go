package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/client"
)

// The macOS menu bar app (desktop/macos): built on macOS by CI and served by
// the server next to the client binaries (<dist>/hearth-menubar-darwin.zip).
const menubarZip = "hearth-menubar-darwin.zip"

func appDir() string {
	h, _ := os.UserHomeDir()
	return filepath.Join(h, "Applications", "Hearth.app")
}

func appCmd() *cobra.Command {
	c := &cobra.Command{Use: "app", Short: "The macOS menu bar app: your machines at a glance, and the console in a window"}
	var dataJSON bool
	data := &cobra.Command{Use: "data", Short: "Print the account's overview, library and teams as JSON (the macOS app's own pages read it)", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		if !dataJSON {
			return errors.New("use --json")
		}
		e, err := engine()
		if err != nil {
			return err
		}
		docs, errs, err := e.Portal()
		if err != nil {
			return err
		}
		return printJSON(map[string]any{"docs": docs, "errors": errs})
	}}
	data.Flags().BoolVar(&dataJSON, "json", false, "machine-readable")
	c.AddCommand(data)
	var adminJSON bool
	adminData := &cobra.Command{Use: "admin-data", Short: "Print the administration pages' documents as JSON (admin accounts only; the macOS app reads it)", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
		if !adminJSON {
			return errors.New("use --json")
		}
		e, err := engine()
		if err != nil {
			return err
		}
		docs, errs, err := e.AdminPortal()
		if err != nil {
			return err
		}
		return printJSON(map[string]any{"docs": docs, "errors": errs})
	}}
	adminData.Flags().BoolVar(&adminJSON, "json", false, "machine-readable")
	callRun := func(admin bool) func(*cobra.Command, []string) error {
		return func(_ *cobra.Command, args []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			var body json.RawMessage
			if len(args) == 3 {
				if !json.Valid([]byte(args[2])) {
					return errors.New("the body is not valid JSON")
				}
				body = json.RawMessage(args[2])
			}
			var out json.RawMessage
			if admin {
				out, err = e.AdminCall(args[0], args[1], body)
			} else {
				out, err = e.ConsoleCall(args[0], args[1], body)
			}
			if err != nil {
				return err
			}
			if len(out) == 0 {
				out = json.RawMessage("{}")
			}
			fmt.Println(string(out))
			return nil
		}
	}
	adminCall := &cobra.Command{Use: "admin-call METHOD PATH [JSON-BODY]", Short: "Send one request to /v1/admin/... as the signed-in admin and print the JSON reply (the macOS app's admin actions use it)", Args: cobra.RangeArgs(2, 3), RunE: callRun(true)}
	call := &cobra.Command{Use: "call METHOD PATH [JSON-BODY]", Short: "Send one request to /v1/me... or /v1/admin/... as the signed-in user and print the JSON reply (the macOS app's actions use it)", Args: cobra.RangeArgs(2, 3), RunE: callRun(false)}
	c.AddCommand(adminData, adminCall, call)
	install := &cobra.Command{Use: "install", Short: "Install (or update) the menu bar app from your server into ~/Applications and start it", RunE: func(*cobra.Command, []string) error {
		e, err := engine()
		if err != nil {
			return err
		}
		dst, err := installMenuBar(e)
		if err != nil {
			return err
		}
		fmt.Printf("installed %s and started it: the flame in the menu bar\n", dst)
		return nil
	}}
	open := &cobra.Command{Use: "open", Short: "Start the menu bar app", RunE: func(*cobra.Command, []string) error {
		if _, err := os.Stat(appDir()); err != nil {
			return errors.New("not installed: hearth app install")
		}
		return exec.Command("open", appDir()).Run()
	}}
	uninstall := &cobra.Command{Use: "uninstall", Short: "Quit and remove the menu bar app", RunE: func(*cobra.Command, []string) error {
		_ = exec.Command("pkill", "-x", "Hearth").Run()
		if err := os.RemoveAll(appDir()); err != nil {
			return err
		}
		fmt.Println("removed", appDir())
		return nil
	}}
	c.AddCommand(install, open, uninstall)
	return c
}

func fileSHA256(p string) (string, error) { return client.FileSHA256(p) }

// errAppNotServed: the server has no menu bar app to hand out.
var errAppNotServed = errors.New("this server does not serve the menu bar app")

// installMenuBar downloads the menu bar app from the logged-in server into
// ~/Applications (replacing an older copy), removes the download quarantine
// and starts it. It returns where the app is.
func installMenuBar(e *client.Engine) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.New("the menu bar app runs on macOS")
	}
	hc, err := client.HTTPClient(e.Home)
	if err != nil {
		return "", err
	}
	hc.Timeout = 5 * time.Minute
	base := client.NewAPI(e.Cfg.Server, "", hc).Base
	resp, err := hc.Get(base + "/v1/dist")
	if err != nil {
		return "", err
	}
	var m struct {
		Apps map[string]string `json:"apps"`
	}
	err = json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	want := m.Apps[menubarZip]
	if err != nil || want == "" {
		return "", fmt.Errorf("%w: %s (its admin puts %s into the dist directory)", errAppNotServed, e.Cfg.Server, menubarZip)
	}
	resp, err = hc.Get(base + "/download/" + menubarZip)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: status %d", resp.StatusCode)
	}
	tmp, err := os.CreateTemp("", "hearth-app-*.zip")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, resp.Body); err != nil {
		tmp.Close()
		return "", err
	}
	tmp.Close()
	if got, err := fileSHA256(tmp.Name()); err != nil || got != want {
		return "", fmt.Errorf("checksum mismatch for the downloaded app")
	}
	dst := appDir()
	_ = exec.Command("pkill", "-x", "Hearth").Run() // the running copy, if any
	stage := dst + ".new"
	_ = os.RemoveAll(stage)
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return "", err
	}
	if out, err := exec.Command("ditto", "-x", "-k", tmp.Name(), stage).CombinedOutput(); err != nil {
		return "", fmt.Errorf("unzip: %v %s", err, out)
	}
	src := filepath.Join(stage, "Hearth.app")
	if _, err := os.Stat(src); err != nil {
		return "", fmt.Errorf("the archive has no Hearth.app")
	}
	_ = os.RemoveAll(dst)
	if err := os.Rename(src, dst); err != nil {
		return "", err
	}
	_ = os.RemoveAll(stage)
	_ = exec.Command("xattr", "-dr", "com.apple.quarantine", dst).Run()
	if err := exec.Command("open", dst).Run(); err != nil {
		return dst, fmt.Errorf("installed in %s, but it did not start: %v", dst, err)
	}
	return dst, nil
}

// wantMenuBarApp: `hearth setup` on a Mac that has no menu bar app yet
// installs it (HEARTH_NO_APP=1 or --no-app skips). An installed app is
// updated with `hearth app install`.
func wantMenuBarApp(goos string, noApp bool, envNoApp string, installed bool) bool {
	return goos == "darwin" && !noApp && envNoApp != "1" && !installed
}
