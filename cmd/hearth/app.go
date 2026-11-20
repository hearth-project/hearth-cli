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
	"regexp"
	"runtime"
	"strings"
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
	var check, jsonOut, yes bool
	var currentVersion string
	update := &cobra.Command{
		Use:   "update",
		Short: "Check for a newer macOS menu bar app and install it after confirmation",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			if runtime.GOOS != "darwin" {
				return errors.New("the menu bar app runs on macOS")
			}
			e, err := engine()
			if err != nil {
				return err
			}
			if currentVersion == "" {
				currentVersion = installedMenuBarVersion()
			}
			info, err := menuBarUpdate(e, currentVersion)
			if err != nil {
				return err
			}
			if jsonOut {
				if err := printJSON(info); err != nil {
					return err
				}
			} else if check || !info.Available {
				if info.Available {
					fmt.Printf("a newer Hearth app is available: %s (current %s)\n", info.LatestVersion, displayVersion(info.CurrentVersion))
				} else if info.LatestVersion == "" {
					fmt.Println("the server does not serve the macOS menu bar app")
				} else {
					fmt.Printf("Hearth app is up to date (%s)\n", displayVersion(info.CurrentVersion))
				}
			}
			if check || !info.Available {
				return nil
			}
			if !yes {
				return fmt.Errorf("Hearth app %s is available; run `hearth app update --yes` to install it", info.LatestVersion)
			}
			dst, err := installMenuBar(e)
			if err != nil {
				return err
			}
			if !jsonOut {
				fmt.Printf("updated %s to %s and started it: the flame in the menu bar\n", dst, info.LatestVersion)
			}
			return nil
		},
	}
	update.Flags().BoolVar(&check, "check", false, "only check whether an update is available")
	update.Flags().BoolVar(&jsonOut, "json", false, "print the result as JSON")
	update.Flags().BoolVar(&yes, "yes", false, "download, replace and restart the app")
	update.Flags().StringVar(&currentVersion, "current-version", "", "version currently running in the app bundle")
	c.AddCommand(update)
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

type menuBarManifest struct {
	Apps       map[string]string `json:"apps"`
	Version    string            `json:"version"`
	AutoUpdate *bool             `json:"auto_update,omitempty"`
}

type menuBarUpdateInfo struct {
	Installed      bool   `json:"installed"`
	CurrentVersion string `json:"current_version,omitempty"`
	LatestVersion  string `json:"latest_version,omitempty"`
	Available      bool   `json:"available"`
	SHA256         string `json:"sha256,omitempty"`
}

func displayVersion(v string) string {
	if strings.TrimSpace(v) == "" {
		return "未知版本"
	}
	return v
}

func installedMenuBarVersion() string {
	b, err := os.ReadFile(filepath.Join(appDir(), "Contents", "Info.plist"))
	if err != nil {
		return ""
	}
	re := regexp.MustCompile(`(?s)<key>CFBundleShortVersionString</key>\s*<string>([^<]+)</string>`)
	m := re.FindSubmatch(b)
	if len(m) != 2 {
		return ""
	}
	return strings.TrimSpace(string(m[1]))
}

func fetchMenuBarManifest(e *client.Engine) (menuBarManifest, *http.Client, string, error) {
	hc, err := client.HTTPClient(e.Home)
	if err != nil {
		return menuBarManifest{}, nil, "", err
	}
	hc.Timeout = 5 * time.Minute
	base := client.NewAPI(e.Cfg.Server, "", hc).Base
	resp, err := hc.Get(base + "/v1/dist")
	if err != nil {
		return menuBarManifest{}, nil, "", err
	}
	var m menuBarManifest
	decodeErr := json.NewDecoder(resp.Body).Decode(&m)
	resp.Body.Close()
	if decodeErr != nil {
		return menuBarManifest{}, nil, "", decodeErr
	}
	if resp.StatusCode != http.StatusOK {
		return menuBarManifest{}, nil, "", fmt.Errorf("server distribution: status %d", resp.StatusCode)
	}
	return m, hc, base, nil
}

func menuBarUpdate(e *client.Engine, current string) (menuBarUpdateInfo, error) {
	m, _, _, err := fetchMenuBarManifest(e)
	if err != nil {
		return menuBarUpdateInfo{}, err
	}
	want := m.Apps[menubarZip]
	info := menuBarUpdateInfo{
		Installed:      installedMenuBarVersion() != "" || appPathExists(appDir()),
		CurrentVersion: current,
		LatestVersion:  m.Version,
		SHA256:         want,
	}
	if want == "" {
		return info, nil
	}
	info.Available = client.VersionNewer(current, m.Version)
	return info, nil
}

func appPathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// installMenuBar downloads the menu bar app from the logged-in server into
// ~/Applications (replacing an older copy), removes the download quarantine
// and starts it. It returns where the app is.
func installMenuBar(e *client.Engine) (string, error) {
	if runtime.GOOS != "darwin" {
		return "", errors.New("the menu bar app runs on macOS")
	}
	m, hc, base, err := fetchMenuBarManifest(e)
	if err != nil {
		return "", err
	}
	want := m.Apps[menubarZip]
	if want == "" {
		return "", fmt.Errorf("%w: %s (its admin puts %s into the dist directory)", errAppNotServed, e.Cfg.Server, menubarZip)
	}
	resp, err := hc.Get(base + "/download/" + menubarZip)
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
	_ = exec.Command("pkill", "-x", "Hearth").Run() // stop the running copy, if any
	backup := dst + ".old"
	_ = os.RemoveAll(backup)
	if _, err := os.Stat(dst); err == nil {
		if err := os.Rename(dst, backup); err != nil {
			return "", fmt.Errorf("stage old app: %w", err)
		}
	}
	if err := os.Rename(src, dst); err != nil {
		_ = os.Rename(backup, dst)
		return "", err
	}
	_ = os.RemoveAll(stage)
	_ = os.RemoveAll(backup)
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
