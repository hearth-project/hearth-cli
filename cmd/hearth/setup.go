package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/library"
	"github.com/hearth-project/hearth-cli/internal/recipe"
)

// setupCmd is the one-shot "new machine" flow run by install.sh:
// log in (if needed), restore every session, start the background daemon.
func setupCmd() *cobra.Command {
	var server, email, device, caCert, invite, team string
	var insecure, noDaemon, force, yes, create, switchLogin, noRegister, noImport bool
	setupLast := defaultRestoreLast
	c := &cobra.Command{
		Use:   "setup",
		Short: "Log in, restore the newest conversations, skills and credentials, and start background sync (new machine)",
		RunE: func(*cobra.Command, []string) error {
			home := client.Home()
			if server == "" {
				if server = client.DefaultServer(home); server == "" {
					return errors.New("which server? pass --server <url> (the address your admin gave you, as in the install command)")
				}
			}
			cfg, err := client.LoadConfig(home)
			if email == "" {
				// Same as login: the account comes from this machine's local
				// identity (existing login, SSO token, Comate login, ...).
				if prepErr := prepareServer(server, caCert, insecure); prepErr != nil {
					return prepErr
				}
				_, _ = client.SiteSyncFrom(home, server)
				detect := client.DetectIdentity
				if switchLogin {
					detect = client.DetectLocalIdentity
				}
				if id, ok := detect(home); ok && id.Email != "" {
					email = id.Email
					fmt.Printf("      local identity: %s (%s)\n", id.Email, id.Source)
				} else if isTTY() {
					// Nothing on this machine says who the user is: ask.
					a, _ := askLine("      your account email (new or existing): ")
					if !strings.Contains(a, "@") {
						return errors.New("no email given; run again with --email <you@example.com>")
					}
					email = a
				} else {
					return errors.New("no local identity found on this machine; pass --email <you@example.com>")
				}
			}
			switch {
			case err == nil && sameServer(cfg.Server, server) && strings.EqualFold(cfg.Email, email):
				fmt.Printf("[1/3] already logged in as %s on %s\n", cfg.Email, cfg.Server)
				_ = client.AutoEnableIdentityLogin(home)
			case err == nil && !switchLogin:
				return client.CheckSwitch(home, server, email)
			case err == nil || errors.Is(err, client.ErrNotLoggedIn):
				if err := prepareServer(server, caCert, insecure); err != nil {
					return err
				}
				// Organisation defaults are public on the server: take them
				// before logging in (the installer usually did already).
				_, _ = client.SiteSyncFrom(home, server)
				if device == "" {
					device, _ = os.Hostname()
				}
				if create || invite != "" {
					switch mode := client.Registration(home, server); {
					case mode == "closed":
						return fmt.Errorf("%s does not accept new accounts; ask its admin", server)
					case mode == "invite" && invite == "":
						return fmt.Errorf("%s needs an invite code for new accounts (ask its admin: hearth server invite); pass --invite", server)
					}
				}
				loggedIn := false
				if !create && invite == "" {
					user, _, _ := strings.Cut(email, "@")
					if secret, ok := client.PlatformSecret(client.LocalIdentity{Username: user, Email: email}); ok {
						if nc, err := client.LoginWithIdentity(home, server, email, secret, device); err == nil {
							cfg, loggedIn = nc, true
							fmt.Printf("[1/3] logged in as %s via the platform identity\n", email)
						} else if st := client.StatusOf(err); st == 0 || st >= 500 {
							fmt.Fprintf(os.Stderr, "      platform identity unavailable (%v); asking for the master password\n", err)
						}
					}
				}
				if !loggedIn {
					pw, err := readPassword("Master password: ", create || invite != "")
					if err != nil {
						return err
					}
					if create || invite != "" {
						// First machine of a new account: register, then log in.
						rk, err := client.Register(home, server, email, pw, invite)
						switch {
						case err == nil:
							fmt.Printf("[1/3] account %s created. Recovery key (shown once; store it offline, e.g. in a password manager,\n"+
								"      and never paste it into a chat or ticket. Lost or leaked: hearth recovery-key rotate):\n      %s\n", email, rk)
						case client.StatusOf(err) == 409:
							fmt.Println("[1/3] account already exists; invite not used")
						default:
							return err
						}
					}
					fmt.Printf("[1/3] logging in to %s as %s\n", server, email)
					cfg, err = client.Login(home, server, email, pw, device)
					if errors.Is(err, client.ErrBadLogin) && !create && invite == "" && !noRegister &&
						(client.Registration(home, server) == "open" || client.Registration(home, server) == "approval") {
						// No account with this password: on an open server that
						// usually means a new user. Register them right here.
						if cfg, err = registerOnSetup(home, server, email, pw, device); err != nil {
							return err
						}
					}
					if err != nil {
						return err
					}
					_ = client.AutoEnableIdentityLogin(home)
				}
				fmt.Printf("      device %s (%s) registered\n", cfg.DeviceName, cfg.DeviceID)
			default:
				return err
			}

			e, err := engine()
			if err != nil {
				return err
			}
			fmt.Println("[2/3] restoring sessions, the skills/agents library and credentials (software: hearth recipe plan)")
			if err := pullAndRenderThen(e, false, func() { chooseMachineAgents(e) }); err != nil {
				fmt.Println("      library:", err)
				chooseMachineAgents(e)
			}
			if created, err := client.EnsureUserConfig(home); err != nil {
				fmt.Println("      config:", err)
			} else if created {
				fmt.Println("      config: created " + library.Library{Root: client.LibraryDir(home)}.ConfigPath() + " (all settings live here)")
			}
			// Organisation defaults the admin publishes on the server
			// (internal hosts, gateway recipes, token locations).
			if r, err := e.SiteSync(); err != nil {
				fmt.Println("      site defaults:", err)
			} else if r.Op == "pulled" {
				fmt.Println("      site defaults from the server: applied with your recipes")
			}
			// The pulled library carries the user's backup selection; a new
			// account gets the default (credentials undecided).
			if created, err := e.EnsureBackupConfig(); err != nil {
				fmt.Println("      backup selection:", err)
			} else if created {
				fmt.Println("      backup selection: default created (hearth backup status / edit)")
			}
			// The pulled library may define this kind of machine and its workspace.
			if ws, err := client.EnsureProfileWorkspace(home); err != nil {
				fmt.Println("      workspace:", err)
			} else if ws != "" {
				fmt.Println("      workspace of this machine's profile:", ws)
			}
			printMachine(home, "      ")
			setupTeam(e, team)
			// Sessions after the library: machine profiles decide the
			// workspace, which path translation and workspace files need.
			restoreAskOnline = isTTY()
			err = restoreAll(e, "", force, setupLast, nil)
			restoreAskOnline = false
			if err != nil {
				return err
			}
			conversations := lastRestore.Written
			conversations += askRestoreOnline(e, force, setupLast)
			secretFiles := 0
			var envLeft []string
			rs, err := e.RestoreSecretFiles(false)
			for _, r := range rs {
				fmt.Printf("secret %-24s %s %s\n", r.Name, r.Action, r.Path)
				if r.Action == "written" {
					secretFiles++
				}
			}
			if err != nil {
				fmt.Println("      secrets:", err)
			}
			if rs, err := e.RestoreShell(); err != nil {
				fmt.Println("      shell:", err)
			} else {
				for _, r := range rs {
					if r.Paragraphs > 0 {
						fmt.Printf("      shell: %d paragraph(s) of your %s, sourced from %s (open a new shell)\n", r.Paragraphs, r.File.Path, r.Hooked)
					}
					if len(r.Missing) > 0 {
						fmt.Printf("      shell: not on this machine, skipped: %s\n", joinMax(r.Missing, 4))
					}
				}
			}
			// Credentials and config files of the recipes, and their shell
			// environment. Software (agent CLIs, gateways, services) is not
			// installed here: the hearth skill goes through this machine's
			// plan with the user (hearth recipe plan).
			if res, err := e.RunRecipes(nil, recipe.Options{Mode: recipe.Restore}); err != nil {
				fmt.Println("      recipes:", err)
			} else {
				for _, r := range res {
					switch {
					case r.Op == "applied" && strings.Contains(r.Step, ":file "):
						secretFiles++
						fmt.Printf("      %s\n", strings.TrimSpace(r.Recipe+" "+r.Step+": "+r.Detail))
					case r.Op == "failed" || r.Op == "warn":
						fmt.Printf("      %s %s / %s: %s\n", r.Op, r.Recipe, r.Step, r.Detail)
					}
				}
			}
			if plan, err := e.Plan(); err == nil {
				for _, p := range plan {
					if p.Status == "todo" && !(p.Type == "check" && p.Why == "optional") {
						envLeft = append(envLeft, planLabel(p))
					}
				}
			}

			// After the recipes: registering the MCP server creates the
			// agent's config file (~/.codex/config.toml), which would stop a
			// recorded one from being restored.
			if done, err := installSkillMCP(true); err != nil {
				fmt.Println("      mcp:", err)
			} else if len(done) > 0 {
				fmt.Println("      skill search and memory MCP: registered with " + strings.Join(done, ", "))
			}
			// Long-term memory: index every machine's conversations here and
			// let the agents' prompt hooks use it.
			if client.MemoryOn(home) {
				if ms, err := e.IndexMemory(); err != nil {
					fmt.Println("      memory:", err)
				} else {
					fmt.Printf("      memory: %d conversation(s), %d fact(s), %d note(s) indexed for recall\n", ms.Conversations, ms.Facts, ms.Notes)
				}
				if res, err := client.SyncMemoryHooks(home, client.MemoryHooksOn(home)); err == nil {
					for _, r := range res {
						if r.Op == "installed" {
							fmt.Printf("      memory hook: %s (%s)\n", r.Agent, r.Path)
							if r.Agent == "codex" {
								fmt.Println("        Codex asks once to trust it at its next start: choose \"Trust all and continue\"")
							}
						}
					}
				}
			}

			// Record this machine for the next one: its skills and agents,
			// new findings, shell lines.
			if !noImport {
				askAutoCredentials(e)
				autoRecord(e, func(f string, a ...any) { fmt.Printf("      "+f+"\n", a...) })
			}
			if noDaemon {
				fmt.Println("[3/3] skipped daemon (--no-daemon); run `hearth daemon start` later")
			} else {
				fmt.Println("[3/3] starting background sync")
				if err := startDaemon(); err != nil {
					return err
				}
			}
			// SSH from the user's other devices (Linux machines, on by default).
			if _, err := e.PublishDeviceKey(); err != nil {
				fmt.Println("      ssh key:", err)
			}
			if client.SSHOn(home) {
				if ss, err := e.EnsureSSHD(); err != nil {
					fmt.Println("      ssh:", err, "(hearth ssh status)")
				} else if ss.Running {
					alias := client.MachineAlias(e.Cfg.DeviceName, factsMap())
					fmt.Printf("      ssh: this machine accepts your devices at %s:%d as %s (Codex Desktop / VS Code / Cursor on a Mac with hearth list it; off: hearth ssh disable)\n",
						ss.Address, ss.Port, alias)
				}
			}
			// The user's apps on this machine (a Mac) list the other machines.
			if n, path, err := writeSSHConfig(e, false); err == nil && n > 0 {
				fmt.Printf("      ssh: %d machine(s) in %s for Codex Desktop (Connections > SSH), VS Code and Cursor: hearth ssh lists them\n", n, path)
			}
			withSkill, err := installSetupSkillTo(false, false)
			if err != nil {
				fmt.Println("      skills:", err)
			}
			printSetupSummary(e, conversations, secretFiles, envLeft, !noDaemon, withSkill)
			return nil
		},
	}
	c.Flags().StringVar(&server, "server", "", "server URL (default: this login's, $HEARTH_SERVER, or the server hearth was installed from)")
	c.Flags().StringVar(&email, "email", "", "account email (default: this machine's local identity, as hearth login)")
	c.Flags().StringVar(&device, "device", "", "device name (default: hostname)")
	c.Flags().StringVar(&caCert, "ca-cert", "", "PEM certificate to trust for a self-signed server")
	c.Flags().BoolVar(&create, "new", false, "create the account first (first machine only; servers in invite mode also need --invite)")
	c.Flags().BoolVar(&switchLogin, "switch", false, "replace an existing login to another account or server")
	c.Flags().StringVar(&invite, "invite", "", "invite code: create the account first (first machine only)")
	c.Flags().BoolVar(&insecure, "insecure", false, "allow plain HTTP to a non-loopback server")
	c.Flags().BoolVar(&noDaemon, "no-daemon", false, "do not start background sync")
	c.Flags().BoolVar(&force, "force", false, "overwrite local session files that differ from the server")
	c.Flags().IntVar(&setupLast, "sessions", setupLast, "restore this many most recently active conversations (0 = all)")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "unused: setup no longer runs recipe commands (the hearth skill goes through hearth recipe plan)")
	_ = c.Flags().MarkHidden("yes")
	c.Flags().BoolVar(&noImport, "no-import", false, "do not record this machine for new ones (its skills and agents, new setup items, shell lines)")
	c.Flags().BoolVar(&noRegister, "no-register", false, "never create the account; fail when the login does not exist")
	c.Flags().StringVar(&team, "team", "", "join a team with this invite code, or create a team of this name (asked in a terminal when you have none)")
	return c
}

// registerOnSetup creates the account after a failed login on an open
// server. The password is typed again first: a typo would otherwise create
// a second, unreachable account instead of reporting a wrong password, and
// an existing account (wrong password) is reported as such.
func registerOnSetup(home, server, email string, pw []byte, device string) (*client.Config, error) {
	fmt.Printf("      login failed: wrong master password, or no account %s yet.\n"+
		"      Already have an account? Press Ctrl-C and run setup again (forgotten: hearth recover).\n", email)
	again, err := readPasswordVar("HEARTH_PASSWORD", "New here? Type the password again to create the account: ", false)
	if err != nil {
		return nil, err
	}
	if string(again) != string(pw) {
		return nil, errors.New("passwords differ; nothing created. Run setup again")
	}
	rk, err := client.Register(home, server, email, pw, "")
	if client.StatusOf(err) == 409 {
		return nil, fmt.Errorf("%s already has an account and the master password is wrong; run setup again (forgotten: hearth recover)", email)
	}
	if err != nil {
		return nil, err
	}
	fmt.Printf("[1/3] account %s created. Recovery key (shown once; store it offline, e.g. in a password manager,\n"+
		"      and never paste it into a chat or ticket. Lost or leaked: hearth recovery-key rotate):\n      %s\n", email, rk)
	cfg, err := client.Login(home, server, email, pw, device)
	if client.StatusOf(err) == 403 && client.Registration(home, server) == "approval" {
		return nil, errors.New("the account waits for the server admin's approval; run the same hearth setup again once it is approved")
	}
	return cfg, err
}

func sameServer(a, b string) bool { return strings.TrimRight(a, "/") == strings.TrimRight(b, "/") }

// chooseMachineAgents: a new machine restores the agents the user worked
// with recently; the others are listed and pulled on request.
func chooseMachineAgents(e *client.Engine) {
	home := e.Home
	if client.HasMachineAgents(home) {
		return
	}
	all, err := e.List()
	if err != nil {
		return
	}
	use := client.AgentUse(all)
	recent, stale := client.RecentAgents(home, use)
	if err := client.SetMachineAgents(home, recent); err != nil {
		fmt.Println("      agents:", err)
		return
	}
	if len(use) == 0 {
		fmt.Printf("      agents set up here: %s (no conversations yet to tell which you use)\n", strings.Join(recent, ", "))
	} else {
		fmt.Printf("      agents restored here (used recently): %s\n", strings.Join(recent, ", "))
	}
	for _, a := range stale {
		fmt.Printf("      %s: last used %s, not restored; when needed: hearth agents pull %s\n", a.Agent, a.Last.Format("2006-01-02"), a.Agent)
	}
}

// setupTeam joins (invite code) or creates (name) a team. Without --team, a
// terminal user who is in no team yet is asked once; Enter skips.
func setupTeam(e *client.Engine, team string) {
	teams, _, err := e.SyncTeamSources()
	if err != nil {
		fmt.Println("      teams:", err)
		return
	}
	if team == "" && len(teams) == 0 && isTTY() {
		a, ok := askLine("      team: paste an invite code to join one, or type a name to create one (Enter = no team for now): ")
		if !ok {
			return
		}
		team = a
	}
	if team == "" {
		if len(teams) > 0 {
			var names []string
			for _, t := range teams {
				names = append(names, t.Name+" ("+t.Role+")")
			}
			fmt.Println("      teams: " + strings.Join(names, ", "))
			applyTeamTop(e, "      ")
		}
		return
	}
	if strings.HasPrefix(team, "team-") {
		if err := joinTeam(e, team); err != nil {
			fmt.Println("      team:", err)
		}
		return
	}
	if err := e.API.CreateTeam(team); err != nil {
		if client.StatusOf(err) == 409 {
			fmt.Printf("      team %s already exists; ask one of its members for an invite (hearth team invite %s), then: hearth team join <code>\n", team, team)
		} else {
			fmt.Println("      team:", err)
		}
		return
	}
	fmt.Printf("      created team %s (owner); invite colleagues: hearth team invite %s; share skills: hearth team share %s <skill> ...\n", team, team, team)
	syncTeams(e)
}

// factsMap is this machine's facts in the shape devices report them.
func factsMap() map[string]any {
	b, _ := json.Marshal(client.Facts())
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}
