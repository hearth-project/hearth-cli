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
	install := &cobra.Command{Use: "install", Short: "Install (or update) the menu bar app from your server into ~/Applications and start it", RunE: func(*cobra.Command, []string) error {
		if runtime.GOOS != "darwin" {
			return errors.New("the menu bar app runs on macOS")
		}
		e, err := engine()
		if err != nil {
			return err
		}
		hc, err := client.HTTPClient(e.Home)
		if err != nil {
			return err
		}
		hc.Timeout = 5 * time.Minute
		base := client.NewAPI(e.Cfg.Server, "", hc).Base
		resp, err := hc.Get(base + "/v1/dist")
		if err != nil {
			return err
		}
		var m struct {
			Apps map[string]string `json:"apps"`
		}
		err = json.NewDecoder(resp.Body).Decode(&m)
		resp.Body.Close()
		want := m.Apps[menubarZip]
		if err != nil || want == "" {
			return fmt.Errorf("%s does not serve the menu bar app (its admin puts %s into the dist directory)", e.Cfg.Server, menubarZip)
		}
		resp, err = hc.Get(base + "/download/" + menubarZip)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("download: status %d", resp.StatusCode)
		}
		tmp, err := os.CreateTemp("", "hearth-app-*.zip")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := io.Copy(tmp, resp.Body); err != nil {
			tmp.Close()
			return err
		}
		tmp.Close()
		if got, err := fileSHA256(tmp.Name()); err != nil || got != want {
			return fmt.Errorf("checksum mismatch for the downloaded app")
		}
		dst := appDir()
		_ = exec.Command("pkill", "-x", "Hearth").Run() // the running copy, if any
		stage := dst + ".new"
		_ = os.RemoveAll(stage)
		if err := os.MkdirAll(stage, 0o755); err != nil {
			return err
		}
		if out, err := exec.Command("ditto", "-x", "-k", tmp.Name(), stage).CombinedOutput(); err != nil {
			return fmt.Errorf("unzip: %v %s", err, out)
		}
		src := filepath.Join(stage, "Hearth.app")
		if _, err := os.Stat(src); err != nil {
			return fmt.Errorf("the archive has no Hearth.app")
		}
		_ = os.RemoveAll(dst)
		if err := os.Rename(src, dst); err != nil {
			return err
		}
		_ = os.RemoveAll(stage)
		_ = exec.Command("xattr", "-dr", "com.apple.quarantine", dst).Run()
		if err := exec.Command("open", dst).Run(); err != nil {
			return fmt.Errorf("installed in %s, but it did not start: %v", dst, err)
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
