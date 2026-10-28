package main

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

// panelCmd prints (and opens) a sign-in link to the server's admin panel
// for an admin logged in on this machine.
func panelCmd() *cobra.Command {
	var noOpen bool
	c := &cobra.Command{
		Use:   "panel",
		Short: "Open the web console: your machines, skills, agents and teams (admins also manage the server)",
		Long: "Asks the server for a sign-in link (valid 10 minutes) for the account logged in here, prints it and\n" +
			"opens it in a browser when there is one. You can also open <server>/app and sign in with email and master\n" +
			"password. Admins (hearth server admin grant <email>) also see the administration pages.",
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			path, err := e.API.AdminLink()
			if err != nil {
				return err
			}
			link := strings.TrimRight(e.Cfg.Server, "/") + path
			fmt.Printf("console sign-in link (valid 10 minutes, keep it private):\n\n  %s\n\n", link)
			if !noOpen {
				openBrowser(link)
			}
			fmt.Println("self-signed server: the browser warns about the certificate once; the console is " + strings.TrimRight(e.Cfg.Server, "/") + "/app")
			return nil
		},
	}
	c.Flags().BoolVar(&noOpen, "no-open", false, "only print the link")
	return c
}

// openBrowser tries the desktop's opener; silently does nothing on
// headless machines.
func openBrowser(u string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "linux":
		if os.Getenv("DISPLAY") == "" && os.Getenv("WAYLAND_DISPLAY") == "" {
			return
		}
		cmd = exec.Command("xdg-open", u)
	default:
		return
	}
	_ = cmd.Start()
}
