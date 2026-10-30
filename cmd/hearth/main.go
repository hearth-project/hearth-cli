// Command hearth syncs coding-agent sessions across machines with
// end-to-end encryption. See docs in the repository README.
package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/hearth-project/hearth-cli/internal/client"
)

func main() {
	client.Version = version
	root := &cobra.Command{
		Use:   "hearth",
		Short: "Hearth: a home for your coding agents (sessions, skills, credentials and setup, end-to-end encrypted)",
		Long: "Hearth: a home for your coding agents (sessions, skills, credentials and setup, end-to-end encrypted)\n\n" +
			"  new machine:  hearth setup          everyday:  hearth status · hearth restore\n" +
			"  or just ask your agent: \"用 hearth 恢复上一台机器的会话\"",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}
	var workspace string
	root.PersistentFlags().StringVar(&workspace, "workspace", "", "your working directory (state goes to <dir>/.hearth; default: found from the current directory)")
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		if workspace != "" {
			home := client.WorkspaceHome(workspace)
			if client.WorkspaceOf(home) == "" {
				var err error
				if home, err = client.InitWorkspace(workspace); err != nil {
					return err
				}
			}
			os.Setenv("HEARTH_HOME", home)
		}
		if cmd.Name() != "server" && (cmd.Parent() == nil || cmd.Parent().Name() != "server") {
			client.Activate()
		}
		return nil
	}
	root.AddCommand(initCmd(), serverCmd(), registerCmd(), loginCmd(), logoutCmd(), statusCmd(),
		whoamiCmd(), identityCmd(),
		pushCmd(), daemonCmd(), restoreCmd(), sessionCmd(), deviceCmd(), setupCmd(), libCmd(), mcpCmd(), secretCmd(), passwdCmd(), recoverCmd(), toolsCmd(),
		recipeCmd(), doctorCmd(), saveCmd(), envCmd(), upCmd(), mcpConfigCmd(), agentsCmd(),
		teamCmd(), sourceCmd(), findCmd(), linkCmd(), unlinkCmd(), linksCmd(), updateCmd(), publishCmd(), learnCmd(), codexThreadsCmd(), codexImportCmd(), agentCmd(), backupSelCmd(), siteCmd(), docsCmd(), selfUpdateCmd(), configCmd(), machineCmd(), onboardCmd(), recoveryKeyCmd(), panelCmd(), shellCmd(), recallCmd(), memoryCmd(), sshCmd())
	groupCommands(root)
	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

// readPassword reads from HEARTH_PASSWORD (for automation) or the
// terminal without echo.
func readPassword(prompt string, confirm bool) ([]byte, error) {
	return readPasswordVar("HEARTH_PASSWORD", prompt, confirm)
}

var stdinLines = bufio.NewReader(os.Stdin)

// readPasswordVar reads a secret from env var, or a line of stdin when it is
// not a terminal, or a hidden terminal prompt.
func readPasswordVar(envVar, prompt string, confirm bool) ([]byte, error) {
	if p := os.Getenv(envVar); p != "" {
		return []byte(p), nil
	}
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		line, err := stdinLines.ReadString('\n')
		if err != nil && line == "" {
			return nil, errors.New("no password on stdin")
		}
		return []byte(strings.TrimRight(line, "\r\n")), nil
	}
	fmt.Fprint(os.Stderr, prompt)
	p, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Fprintln(os.Stderr)
	if err != nil {
		return nil, err
	}
	if confirm {
		fmt.Fprint(os.Stderr, "Repeat password: ")
		q, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Fprintln(os.Stderr)
		if err != nil {
			return nil, err
		}
		if string(p) != string(q) {
			return nil, errors.New("passwords do not match")
		}
	}
	return p, nil
}

func initCmd() *cobra.Command {
	var moveLogin bool
	c := &cobra.Command{
		Use:   "init [dir]",
		Short: "Make a directory your hearth workspace (state in <dir>/.hearth; --move-login brings the login of ~/.hearth)",
		Long: "On machines where several people share one Unix account, each person keeps their state, " +
			"agent config dirs and credentials in their own workspace instead of the shared $HOME.\n\n" +
			"A workspace has its own login. If this machine is already logged in without one (~/.hearth) and the\n" +
			"workspace is yours, --move-login moves that login and its state there (the daemon restarts); otherwise\n" +
			"log in inside the workspace with hearth setup.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(_ *cobra.Command, a []string) error {
			dir := "."
			if len(a) == 1 {
				dir = a[0]
			}
			abs, err := filepath.Abs(dir)
			if err != nil {
				return err
			}
			target := client.WorkspaceHome(abs)
			def := client.DefaultHome()
			_, defErr := client.LoadConfig(def)
			_, tgtErr := client.LoadConfig(target)
			movable := defErr == nil && tgtErr != nil && filepath.Clean(def) != filepath.Clean(target)
			restart := false
			if moveLogin {
				if !movable {
					return fmt.Errorf("nothing to move: %s is not logged in, or %s already is", def, target)
				}
				// A plain hearth init leaves only its marker and the CA copy
				// (and maybe empty directories from `hearth mcp`). Checked
				// before stopping the daemon so a refusal leaves it running.
				if name := client.OwnState(target); name != "" {
					return fmt.Errorf("%s already holds hearth state (%s); move it away first", target, name)
				}
				wasRunning := client.DaemonPID(def) != 0
				if wasRunning {
					if err := client.StopDaemon(def, 30*time.Second); err != nil {
						return fmt.Errorf("stop the daemon of %s: %w", def, err)
					}
				}
				// Renamed, or copied then removed on another disk (cluster
				// storage); rc-file blocks follow it.
				if _, err := client.MoveHome(def, abs); err != nil {
					return fmt.Errorf("move %s to %s: %w", def, target, err)
				}
				fmt.Printf("moved the login and state of %s to %s\n", def, target)
				restart = wasRunning
			}
			home, err := client.InitWorkspace(dir)
			if err != nil {
				return err
			}
			if moveLogin {
				// Links and registrations named the old home: point them here.
				os.Setenv("HEARTH_HOME", home)
				if e, err := client.NewEngine(home); err == nil {
					if _, err := e.Render(false); err != nil {
						fmt.Println("skills and agents:", err)
					}
				}
				if _, err := installSkillMCP(true); err != nil {
					fmt.Println("skill search:", err)
				}
				_, _ = client.SyncMemoryHooks(home, client.MemoryHooksOn(home))
				if restart {
					if err := startDaemon(); err != nil {
						fmt.Println("daemon:", err)
					}
				}
			}
			fmt.Printf("workspace %s (state %s)\n", abs, home)
			if dev, other := client.OtherHostHome(abs); other {
				fmt.Printf("this disk is shared: %s/.hearth is machine %s's, this machine keeps its own in %s\n", abs, orDefault(dev, "another machine"), home)
			}
			if _, err := client.LoadConfig(home); err != nil {
				if movable {
					cfg, _ := client.LoadConfig(def)
					fmt.Printf("not logged in here yet: hearth commands inside the workspace use its own login. ~/.hearth is logged in as %s;\n", cfg.Email)
					fmt.Printf("  yours: hearth init --move-login %s   (moves that login here)\n  or:    cd %s && hearth setup\n", abs, abs)
				} else {
					fmt.Printf("not logged in here yet:  cd %s && hearth setup\n", abs)
				}
			}
			fmt.Println("hearth commands run anywhere inside it use this state. On an account several people share,")
			fmt.Println("keep your Codex config apart from the shared ~/.codex too:  hearth agents isolate codex")
			return nil
		},
	}
	c.Flags().BoolVar(&moveLogin, "move-login", false, "move the login and state of ~/.hearth into this workspace")
	return c
}

func engine() (*client.Engine, error) { return client.NewEngine(client.Home()) }

// prepareServer validates the server URL and installs an optional CA.
func prepareServer(server, caCert string, insecure bool) error {
	if err := client.CheckServerURL(server, insecure); err != nil {
		return err
	}
	if caCert != "" {
		return client.InstallCA(client.Home(), caCert)
	}
	return nil
}

func registerCmd() *cobra.Command {
	var server, email, invite, caCert string
	var insecure bool
	c := &cobra.Command{
		Use:   "register",
		Short: "Create an account (prints a one-time recovery key)",
		RunE: func(*cobra.Command, []string) error {
			if err := prepareServer(server, caCert, insecure); err != nil {
				return err
			}
			pw, err := readPassword("Master password: ", true)
			if err != nil {
				return err
			}
			rk, err := client.Register(client.Home(), server, email, pw, invite)
			if err != nil {
				if client.StatusOf(err) == 403 {
					return fmt.Errorf("%w (ask the server admin for an invite: hearth server invite)", err)
				}
				return err
			}
			fmt.Println("Account created.")
			fmt.Println("Recovery key (shown once, store it offline; it is the only way to recover data if you forget the password):")
			fmt.Println("  " + rk)
			fmt.Println("Next: hearth login --server", server, "--email", email)
			return nil
		},
	}
	c.Flags().StringVar(&server, "server", "", "server URL")
	c.Flags().StringVar(&email, "email", "", "account email")
	c.Flags().StringVar(&invite, "invite", "", "single-use invite code from the server admin")
	c.Flags().StringVar(&caCert, "ca-cert", "", "PEM certificate to trust for a self-signed server")
	c.Flags().BoolVar(&insecure, "insecure", false, "allow plain HTTP to a non-loopback server")
	c.MarkFlagRequired("server")
	c.MarkFlagRequired("email")
	return c
}

func loginCmd() *cobra.Command {
	var server, email, device, caCert string
	var insecure, switchLogin bool
	c := &cobra.Command{
		Use:   "login",
		Short: "Log in and register this machine as a device",
		Long: "Logs in with the account this machine already knows: an existing login, else the local identity\n" +
			"(company SSO token cache, Comate login, platform user name) for --email. Only the password is asked for;\n" +
			"pass --switch to replace another account.",
		RunE: func(*cobra.Command, []string) error {
			home := client.Home()
			cfg, cfgErr := client.LoadConfig(home)
			if server == "" && cfgErr == nil {
				server = cfg.Server
			}
			if server == "" {
				server = client.DefaultServer(home)
			}
			if server == "" {
				return errors.New("which server? pass --server <url> (the address your admin gave you, as in the install command)")
			}
			if err := prepareServer(server, caCert, insecure); err != nil {
				return err
			}
			// The public site defaults say where this organisation keeps its
			// SSO token; take them before looking for a local identity.
			_, _ = client.SiteSyncFrom(home, server)
			if email == "" {
				detect := client.DetectIdentity
				if switchLogin {
					detect = client.DetectLocalIdentity
				}
				if id, ok := detect(home); ok && id.Email != "" {
					email = id.Email
					fmt.Printf("local identity: %s (%s)\n", id.Email, id.Source)
				}
			}
			if email == "" {
				var err error
				if email, err = pickIdentity(home); err != nil {
					return err
				}
			}
			if !switchLogin {
				if err := client.CheckSwitch(home, server, email); err != nil {
					return err
				}
				if cfgErr == nil && sameServer(cfg.Server, server) && strings.EqualFold(cfg.Email, email) &&
					cfg.ExpiresAt > time.Now().Unix() {
					fmt.Printf("Already logged in as %s on %s (device %s, token valid until %s).\n",
						cfg.Email, cfg.Server, cfg.DeviceName, time.Unix(cfg.ExpiresAt, 0).Format("2006-01-02 15:04"))
					_ = client.AutoEnableIdentityLogin(home)
					return nil
				}
			}
			if device == "" {
				device, _ = os.Hostname()
			}
			// Passwordless path: the server's identity broker holds the same
			// platform secret this container was given, so possession proves
			// the account. Anything else falls back to the master password.
			user, _, _ := strings.Cut(email, "@")
			if secret, ok := client.PlatformSecret(client.LocalIdentity{Username: user, Email: email}); ok {
				if nc, err := client.LoginWithIdentity(home, server, email, secret, device); err == nil {
					fmt.Printf("Logged in as %s on device %s (%s) via the platform identity.\n", nc.Email, nc.DeviceName, nc.DeviceID)
					return nil
				} else if st := client.StatusOf(err); st == 0 || st >= 500 {
					fmt.Fprintf(os.Stderr, "platform identity login unavailable (%v); falling back to the master password\n", err)
				}
			}
			pw, err := readPassword("Master password: ", false)
			if err != nil {
				return err
			}
			cfg, err = client.Login(home, server, email, pw, device)
			if err != nil {
				return err
			}
			_ = client.AutoEnableIdentityLogin(home)
			fmt.Printf("Logged in as %s on device %s (%s). State in %s\n", cfg.Email, cfg.DeviceName, cfg.DeviceID, client.Home())
			return nil
		},
	}
	c.Flags().StringVar(&server, "server", "", "server URL (default: the existing login's, else $HEARTH_SERVER)")
	c.Flags().StringVar(&email, "email", "", "account email (default: the local identity)")
	c.Flags().StringVar(&device, "device", "", "device name (default: hostname)")
	c.Flags().StringVar(&caCert, "ca-cert", "", "PEM certificate to trust for a self-signed server")
	c.Flags().BoolVar(&insecure, "insecure", false, "allow plain HTTP to a non-loopback server")
	c.Flags().BoolVar(&switchLogin, "switch", false, "replace an existing login to another account or server")
	return c
}

// whoamiCmd shows what this machine thinks the user is, and which hearth
// account (if any) this workspace is logged in to.
func whoamiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "whoami",
		Short: "Show the local identity and this workspace's hearth login",
		RunE: func(*cobra.Command, []string) error {
			home := client.Home()
			if id, ok := client.DetectLocalIdentity(home); ok {
				line := "local identity: " + id.Username
				if id.Email != "" {
					line += " <" + id.Email + ">"
				}
				fmt.Printf("%s (%s)\n", line, id.Source)
			} else {
				fmt.Println("local identity: not found")
			}
			cfg, err := client.LoadConfig(home)
			if err != nil {
				fmt.Printf("hearth: not logged in (%s)\n", home)
				return nil
			}
			state := "expires " + time.Unix(cfg.ExpiresAt, 0).Format("2006-01-02 15:04")
			if cfg.ExpiresAt <= time.Now().Unix() {
				state = "expired " + time.Unix(cfg.ExpiresAt, 0).Format("2006-01-02 15:04")
			}
			fmt.Printf("hearth: %s on %s, device %s, token %s\n", cfg.Email, cfg.Server, cfg.DeviceName, state)
			return nil
		},
	}
}

// identityCmd manages passwordless login through a platform identity
// broker: the server resolves the account's platform secret and the client
// proves it holds the same one.
func identityCmd() *cobra.Command {
	c := &cobra.Command{
		Use:   "identity",
		Short: "Passwordless login through the platform identity broker",
	}
	c.AddCommand(&cobra.Command{
		Use:   "enable",
		Short: "Upload the platform unlock now (normally automatic)",
		RunE: func(*cobra.Command, []string) error {
			home := client.Home()
			id, ok := client.DetectIdentity(home)
			if !ok || id.Email == "" {
				return errors.New("no local identity found; run this inside the workspace of the account")
			}
			secret, ok := client.PlatformSecret(id)
			if !ok {
				return fmt.Errorf("no platform credential for %s on this container (SYS_USER_NAME=%q); "+
					"run this inside a job container created for that account", id.Username, os.Getenv("SYS_USER_NAME"))
			}
			if err := client.EnableIdentityLogin(home, secret, true); err != nil {
				return err
			}
			fmt.Printf("platform identity login enabled for %s\n", id.Email)
			return nil
		},
	}, &cobra.Command{
		Use:   "disable",
		Short: "Stop allowing passwordless login for this account",
		RunE: func(*cobra.Command, []string) error {
			if err := client.DisableIdentityLogin(client.Home()); err != nil {
				return err
			}
			fmt.Println("platform identity login disabled")
			return nil
		},
	}, &cobra.Command{
		Use:   "status",
		Short: "Show whether a platform credential is available on this machine",
		RunE: func(*cobra.Command, []string) error {
			home := client.Home()
			id, ok := client.DetectIdentity(home)
			if !ok {
				fmt.Println("local identity: not found")
				return nil
			}
			_, has := client.PlatformSecret(id)
			who := id.Email
			if who == "" {
				who = id.Username + " (no mail domain known)"
			}
			fmt.Printf("local identity: %s (%s)\n", who, id.Source)
			fmt.Printf("platform credential: %v (account user %q)\n", has, os.Getenv("SYS_USER_NAME"))
			if st, err := client.IdentityServerStatus(home); err == nil {
				fmt.Printf("hearth identity login: enabled=%v disabled=%v\n", st.Enabled, st.Disabled)
			}
			return nil
		},
	})
	return c
}

func logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove local credentials and cached vault key",
		RunE: func(*cobra.Command, []string) error {
			if err := client.Logout(client.Home()); err != nil {
				return err
			}
			fmt.Println("Logged out. Server data is kept.")
			return nil
		},
	}
}

// groupCommands sorts `hearth --help` into a few sections, most used
// first; plumbing and aliases stay out of the list (still runnable).
func groupCommands(root *cobra.Command) {
	groups := []struct {
		id, title string
		names     []string
	}{
		{"start", "Start here:", []string{"setup", "status", "restore", "config"}},
		{"sessions", "Conversations:", []string{"session", "push", "agents", "backup"}},
		{"library", "Skills and agents:", []string{"lib", "team", "mcp-config"}},
		{"machine", "This machine's setup:", []string{"learn", "recipe", "doctor", "tools", "machine", "secret", "up", "init", "daemon", "self-update"}},
		{"account", "Account:", []string{"login", "logout", "whoami", "device", "passwd", "recover", "recovery-key"}},
		{"server", "Server (admins):", []string{"panel", "server", "site"}},
	}
	byName := map[string]*cobra.Command{}
	for _, c := range root.Commands() {
		byName[c.Name()] = c
	}
	// Listed in the order above, not alphabetically.
	cobra.EnableCommandSorting = false
	rest := root.Commands()
	root.RemoveCommand(rest...)
	placed := map[string]bool{}
	for _, g := range groups {
		root.AddGroup(&cobra.Group{ID: g.id, Title: g.title})
		for _, n := range g.names {
			if c := byName[n]; c != nil {
				c.GroupID = g.id
				root.AddCommand(c)
				placed[n] = true
			}
		}
	}
	for _, c := range rest {
		if !placed[c.Name()] {
			root.AddCommand(c)
		}
	}
	// One home per topic: sharing commands under lib, agent import under
	// agents. The old top-level names keep working (hidden).
	if lib := byName["lib"]; lib != nil {
		lib.AddCommand(findCmd(), linkCmd(), unlinkCmd(), linksCmd(), updateCmd(), publishCmd(), sourceCmd())
	}
	if ag := byName["agents"]; ag != nil {
		for _, sub := range agentCmd().Commands() {
			if sub.Name() == "import" {
				ag.AddCommand(sub)
			}
		}
	}
	// Aliases and plumbing (agents, scripts): hidden from the list,
	// documented in docs/cli.md.
	for _, n := range []string{"codex-import", "codex-threads", "mcp", "agent", "find", "link", "unlink", "links", "update", "publish", "source",
		"onboard", "save", "env", "register", "identity"} {
		if c := byName[n]; c != nil {
			c.Hidden = true
			switch n { // agent plumbing without another documented home
			case "codex-import", "codex-threads", "mcp", "onboard", "save", "env", "register", "identity":
				c.Annotations = map[string]string{"documented": "yes"}
			}
		}
	}
	root.SetHelpCommandGroupID("account")
	root.SetCompletionCommandGroupID("account")
}
