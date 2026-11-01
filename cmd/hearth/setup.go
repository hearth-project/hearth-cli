package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/recipe"
)

// setupCmd is the one-shot "new machine" flow run by install.sh:
// log in (if needed), restore every session, start the background daemon.
func setupCmd() *cobra.Command {
	var server, email, device, caCert, invite, team string
	var installToken string
	var insecure, noDaemon, force, yes, create, switchLogin, noRegister, noImport, full, newLogin bool
	var alias string
	setupLast := defaultRestoreLast
	c := &cobra.Command{
		Use:   "setup",
		Short: "Log in, restore the newest conversations, skills and credentials, and start background sync (new machine)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			home := client.Home()
			out := newSetupOut()
			defer out.close()
			if server == "" {
				if server = client.DefaultServer(home); server == "" {
					return errors.New("which server? pass --server <url> (the address your admin gave you, as in the install command)")
				}
			}
			out.title(setupHeader())
			cfg, err := client.LoadConfig(home)
			if email == "" {
				// Same as login: the account comes from this machine's local
				// identity (existing login, SSO token, Comate login, ...).
				if prepErr := prepareServer(server, caCert, insecure); prepErr != nil {
					return prepErr
				}
				_, _ = client.SiteSyncFrom(home, server)
				reportProxy(out)
				detect := client.DetectIdentity
				if switchLogin {
					detect = client.DetectLocalIdentity
				}
				if id, ok := detect(home); ok && id.Email != "" {
					email = id.Email
					out.line("account", id.Email+" (from "+id.Source+")")
				} else {
					// Nothing says which of the people known here is asking.
					picked, perr := pickIdentity(home)
					if perr != nil {
						return perr
					}
					email = picked
					out.line("account", email)
				}
			}
			if def, ok := client.MovableLogin(home); ok && sameServer(def.Server, server) && strings.EqualFold(def.Email, email) {
				// A second login of the same account on this machine is a
				// second device: both daemons would scan the same sessions.
				ws := client.WorkspaceOf(home)
				return fmt.Errorf("~/.hearth is already logged in as %s on this machine; move that login into this workspace instead:\n"+
					"  hearth init --move-login %s && cd %s && hearth daemon start", def.Email, ws, ws)
			}
			switch {
			case err == nil && sameServer(cfg.Server, server) && strings.EqualFold(cfg.Email, email):
				out.line("login", "already logged in as "+cfg.Email)
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
				reportProxy(out)
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
				loggedIn, via := false, ""
				if installToken == "" {
					installToken = os.Getenv("HEARTH_INSTALL_TOKEN")
				}
				if installToken != "" && !create && invite == "" {
					// One-time install command from the web console: no password.
					if nc, err := client.LoginWithInstallToken(home, server, email, installToken, device); err == nil {
						cfg, loggedIn = nc, true
						via = " (one-time install command, no password needed)"
					} else if !errors.Is(err, client.ErrInstallToken) {
						return err
					} else {
						fmt.Fprintf(os.Stderr, "  %v\n", err)
					}
				}
				if !loggedIn && !create && invite == "" {
					user, _, _ := strings.Cut(email, "@")
					if secret, ok := client.PlatformSecret(client.LocalIdentity{Username: user, Email: email}); ok {
						if nc, err := client.LoginWithIdentity(home, server, email, secret, device); err == nil {
							cfg, loggedIn = nc, true
							via = " (platform identity, no password needed)"
						} else if st := client.StatusOf(err); st == 0 || st >= 500 {
							fmt.Fprintf(os.Stderr, "  platform identity unavailable (%v); asking for the master password\n", err)
						}
					}
				}
				if !loggedIn {
					pw, err := readPassword("  Master password: ", create || invite != "")
					if err != nil {
						return err
					}
					if create || invite != "" {
						// First machine of a new account: register, then log in.
						rk, err := client.Register(home, server, email, pw, invite)
						switch {
						case err == nil:
							printRecoveryKey(email, rk)
						case client.StatusOf(err) == 409:
							out.line("account", "exists already; invite not used")
						default:
							return err
						}
					}
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
				out.line("login", "ok, this machine is device "+cfg.DeviceName+via)
				// Where this person works on this machine: proposed from their
				// machines of the same kind, confirmed (or changed) by them.
				home = chooseSetupWorkspace(out, home, cfg)
				// A new machine restores credentials, conversations, shell
				// lines and agent setup only when asked (--full: everything).
				if err := client.SetOnDemand(home, !full); err != nil {
					out.warn("restore mode", err)
				}
				newLogin = true
			default:
				return err
			}

			// Shared account: this person's Codex config and conversations
			// in their own dir, not in the ~/.codex everybody falls back to.
			if dir := client.PreferOwnCodexHome(home); dir != "" {
				out.line("codex home", dir+" (yours, not the shared ~/.codex)")
			}
			e, err := engine()
			if err != nil {
				return err
			}
			// A name for this machine, shown as "name (host)" everywhere and
			// used as its SSH host name.
			if newLogin || alias != "" {
				chooseSetupAlias(out, e, alias)
			}
			var libErr, restoreErr error
			var teams string
			out.quiet(func() {
				if libErr = pullAndRenderThen(e, false, func() { chooseMachineAgents(e) }); libErr != nil {
					chooseMachineAgents(e)
				}
				if _, err := client.EnsureUserConfig(home); err != nil {
					fmt.Println("config:", err)
				}
				// Organisation defaults the admin publishes on the server
				// (internal hosts, gateway recipes, token locations).
				if _, err := e.SiteSync(); err != nil {
					fmt.Println("site defaults:", err)
				}
				// The pulled library carries the user's backup selection; a
				// new account gets the default (credentials undecided).
				if _, err := e.EnsureBackupConfig(); err != nil {
					fmt.Println("backup selection:", err)
				}
			})
			if libErr != nil {
				out.warn("library", libErr)
			}
			// The pulled library may define this kind of machine and its workspace.
			if ws, err := client.EnsureProfileWorkspace(home); err != nil {
				out.warn("workspace", err)
			} else if ws != "" {
				out.line("workspace", ws+" (this machine's profile)")
			}
			onDemand := client.OnDemand(home)
			sessionsAsked := cmd.Flags().Changed("sessions")
			out.quiet(func() {
				printMachine(home, "")
				teams = setupTeam(e, team)
				// Sessions after the library: machine profiles decide the
				// workspace, which path translation and workspace files need.
				if !onDemand || sessionsAsked {
					restoreErr = restoreAll(e, "", force, setupLast, nil)
				}
			})
			if restoreErr != nil {
				return restoreErr
			}
			conversations, online := lastRestore.Written, len(lastRestore.LeftOnline)
			secretFiles, failed := 0, 0
			var rcs, envLeft []string
			codexTrust := false
			out.quiet(func() {
				if onDemand {
					return // credentials, shell lines and recipe files when asked for
				}
				rs, err := e.RestoreSecretFiles(false)
				for _, r := range rs {
					fmt.Printf("secret %-24s %s %s\n", r.Name, r.Action, r.Path)
					if r.Action == "written" {
						secretFiles++
					}
				}
				if err != nil {
					fmt.Println("secrets:", err)
					failed++
				}
				if rs, err := e.RestoreShell(); err != nil {
					fmt.Println("shell:", err)
				} else {
					for _, r := range rs {
						if r.Paragraphs > 0 {
							fmt.Printf("shell: %d paragraph(s) of your %s, sourced from %s\n", r.Paragraphs, r.File.Path, r.Hooked)
							rcs = append(rcs, r.File.Path)
						}
						if len(r.Missing) > 0 {
							fmt.Printf("shell: not on this machine, skipped: %s\n", joinMax(r.Missing, 4))
						}
					}
				}
				// Credentials and config files of the recipes, and their shell
				// environment. Software (agent CLIs, gateways, services) is not
				// installed here: the hearth skill goes through this machine's
				// plan with the user (hearth recipe plan).
				if res, err := e.RunRecipes(nil, recipe.Options{Mode: recipe.Restore}); err != nil {
					fmt.Println("recipes:", err)
					failed++
				} else {
					for _, r := range res {
						switch {
						case r.Op == "applied" && strings.Contains(r.Step, ":file "):
							secretFiles++
							fmt.Println(strings.TrimSpace(r.Recipe + " " + r.Step + ": " + r.Detail))
						case r.Op == "failed" || r.Op == "warn":
							fmt.Printf("%s %s / %s: %s\n", r.Op, r.Recipe, r.Step, r.Detail)
							failed++
						}
					}
				}
			})
			out.quiet(func() {
				if plan, err := e.Plan(); err == nil {
					for _, p := range plan {
						if p.Status == "todo" && !(p.Type == "check" && p.Why == "optional") {
							envLeft = append(envLeft, planLabel(p))
						}
					}
				}
				// After the recipes: registering the MCP server creates the
				// agent's config file (~/.codex/config.toml), which would stop
				// a recorded one from being restored.
				if done, err := installSkillMCP(true); err != nil {
					fmt.Println("mcp:", err)
				} else if len(done) > 0 {
					fmt.Println("skill search and memory MCP: registered with " + strings.Join(done, ", "))
				}
			})
			// Long-term memory: index every machine's conversations here and
			// let the agents' prompt hooks use it.
			indexed := -1
			if client.MemoryOn(home) && (!onDemand || len(client.MachineAgents(home)) > 0) {
				out.quiet(func() {
					if ms, err := e.IndexMemory(); err != nil {
						fmt.Println("memory:", err)
					} else {
						indexed = ms.Conversations
					}
					if res, err := client.SyncMemoryHooks(home, client.MemoryHooksOn(home)); err == nil {
						for _, r := range res {
							if r.Op == "installed" {
								fmt.Printf("memory hook: %s (%s)\n", r.Agent, r.Path)
								codexTrust = codexTrust || r.Agent == "codex"
							}
						}
					}
				})
			}

			// What this machine has now, one line per kind.
			skills, agents := libraryCounts(home)
			if ma := client.MachineAgents(home); len(ma) > 0 {
				out.line("library", fmt.Sprintf("%s, %s for %s", plural(skills, "skill", "skills"), plural(agents, "agent", "agents"), strings.Join(ma, ", ")))
			} else {
				out.line("library", fmt.Sprintf("%s, %s; set up for an agent once it is installed here", plural(skills, "skill", "skills"), plural(agents, "agent", "agents")))
			}
			if secretFiles > 0 {
				out.line("files", plural(secretFiles, "credential or config file", "credential and config files")+" written")
			}
			if len(rcs) > 0 {
				out.line("shell", "your lines of "+strings.Join(rcs, ", ")+" (new shells)")
			}
			if onDemand && !sessionsAsked {
				out.line("on demand", "conversations (hearth restore --last 10), credentials (your agent asks hearth when a skill needs one),\n"+
					"shell lines (hearth shell restore), agent setup (\"恢复这台机器的 Codex 环境\"); everything now: hearth setup --full")
			} else {
				conv := plural(conversations, "conversation", "conversations") + " restored"
				if online > 0 {
					conv += fmt.Sprintf("; %d more on machines still online: hearth restore --include-online", online)
				}
				out.line("sessions", conv)
			}
			if indexed >= 0 {
				out.line("memory", plural(indexed, "conversation", "conversations")+" indexed for recall")
			}
			if teams != "" {
				out.line("team", teams)
			}
			if failed > 0 {
				out.line("problems", plural(failed, "step", "steps")+" failed: "+orDefault(out.logHint(), "see above"))
			}

			// Record this machine for the next one: its skills and agents,
			// new findings, shell lines.
			if !noImport {
				askAutoCredentials(e)
				out.quiet(func() { autoRecord(e, func(f string, a ...any) { fmt.Printf(f+"\n", a...) }) })
			}
			if noDaemon {
				out.line("sync", "off (--no-daemon): hearth daemon start")
			} else {
				var derr error
				out.quiet(func() { derr = startDaemon() })
				if derr != nil {
					return derr
				}
				out.line("sync", "on (background)")
			}
			// SSH from the user's other devices (Linux machines, on by default).
			out.quiet(func() {
				if _, err := e.PublishDeviceKey(); err != nil {
					fmt.Println("ssh key:", err)
				}
			})
			if client.SSHOn(home) {
				var ss client.SSHStatus
				var serr error
				out.quiet(func() {
					if _, err := client.EnsureIdentityRC(e.Home, e.Cfg.Email); err != nil {
						fmt.Println("identity block:", err)
					}
					ss, serr = e.EnsureSSHD()
				})
				if serr != nil {
					out.warn("ssh", fmt.Errorf("%v (hearth ssh status)", serr))
				} else if ss.Running {
					out.line("ssh", fmt.Sprintf("%s (%s:%d)", thisAlias(e), ss.Address, ss.Port))
				}
			}
			// The user's apps on this machine (a Mac) list the other machines.
			if n, path, err := writeSSHConfig(e, false); err == nil && n > 0 {
				out.line("ssh config", fmt.Sprintf("%s in %s (Codex Desktop, VS Code, Cursor)", plural(n, "machine", "machines"), path))
			}
			var withSkill []string
			out.quiet(func() {
				var err error
				if withSkill, err = installSetupSkillTo(false, false); err != nil {
					fmt.Println("skills:", err)
				}
			})
			printSetupSummary(out, e, envLeft, withSkill, codexTrust)
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
	c.Flags().StringVar(&installToken, "token", "", "one-time install token from the web console's install command: log in without the password (works once, 30 minutes) [$HEARTH_INSTALL_TOKEN]")
	c.Flags().BoolVar(&insecure, "insecure", false, "allow plain HTTP to a non-loopback server")
	c.Flags().BoolVar(&noDaemon, "no-daemon", false, "do not start background sync")
	c.Flags().BoolVar(&force, "force", false, "overwrite local session files that differ from the server")
	c.Flags().IntVar(&setupLast, "sessions", setupLast, "restore this many most recently active conversations (0 = all; a new machine restores none unless given)")
	c.Flags().StringVar(&alias, "alias", "", "a name for this machine (default: asked in a terminal, proposing e.g. gb-node-e05-13); hearth device rename changes it")
	c.Flags().BoolVar(&full, "full", false, "restore everything now (credentials, conversations, shell lines, agent setup) instead of on demand")
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
	fmt.Printf("  login        wrong master password, or no account %s yet.\n"+
		"               Already have an account? Press Ctrl-C and run setup again (forgotten: hearth recover).\n", email)
	again, err := readPasswordVar("HEARTH_PASSWORD", "  New here? Type the password again to create the account: ", false)
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
	printRecoveryKey(email, rk)
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
	if client.OnDemand(home) {
		// Set up an agent here once it is installed (the daemon adopts it).
		recent = client.InstalledAgents(recent)
	}
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
func setupTeam(e *client.Engine, team string) string {
	teams, _, err := e.SyncTeamSources()
	if err != nil {
		fmt.Println("      teams:", err)
		return ""
	}
	if team == "" && len(teams) == 0 && isTTY() {
		a, ok := askLine(fmt.Sprintf("  %-12s paste an invite code to join one, or type a name to create one (Enter = none for now): ", "team"))
		if !ok {
			return ""
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
			return strings.Join(names, ", ")
		}
		return ""
	}
	if strings.HasPrefix(team, "team-") {
		if err := joinTeam(e, team); err != nil {
			fmt.Println("      team:", err)
			return "not joined: " + err.Error()
		}
		return "joined"
	}
	if err := e.API.CreateTeam(team); err != nil {
		if client.StatusOf(err) == 409 {
			fmt.Printf("      team %s already exists; ask one of its members for an invite (hearth team invite %s), then: hearth team join <code>\n", team, team)
		} else {
			fmt.Println("      team:", err)
		}
		return "not created (" + team + ")"
	}
	fmt.Printf("      created team %s (owner); invite colleagues: hearth team invite %s; share skills: hearth team share %s <skill> ...\n", team, team, team)
	syncTeams(e)
	return team + " (created, owner): hearth team invite " + team
}

// factsMap is this machine's facts in the shape devices report them.
func factsMap() map[string]any {
	b, _ := json.Marshal(client.Facts())
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// chooseSetupWorkspace asks, on a fresh login in ~/.hearth, for this
// machine's working directory: the one the user's machines of the same
// kind use, else their home. Another directory than the home moves the
// new login there (<dir>/.hearth, or <dir>/.hearth/hosts/<host> when the
// directory is on a disk another machine logged in to). Returns the home
// to continue with.
func chooseSetupWorkspace(out *setupOut, home string, cfg *client.Config) string {
	if client.WorkspaceOf(home) != "" || (os.Getenv("HEARTH_WORKSPACE") != "" && os.Getenv("HEARTH_WORKSPACE_FROM_PROFILE") == "") {
		return home // chosen already (hearth init, --workspace)
	}
	h, _ := os.UserHomeDir()
	sug := client.WorkspaceSuggestion{}
	if e, err := client.NewEngine(home); err == nil {
		if ds, err := e.API.Devices(); err == nil {
			sug = client.SuggestWorkspace(ds, cfg.DeviceID, client.Facts(), client.WorkspaceUsable)
		}
	}
	def := h
	why := "none of your machines is like this one"
	if sug.Path != "" {
		def = sug.Path
		why = "as on " + shortHost(sug.Machine) + " (" + sug.Kind + ")"
	}
	if sug.Missing != "" {
		// The most alike machine works on a disk this one does not have.
		why += fmt.Sprintf("; %s works in %s, which is not on this machine", shortHost(sug.MissingMachine), sug.Missing)
	}
	if !isTTY() {
		if sug.Path != "" {
			out.line("workspace", fmt.Sprintf("your home for now; %s works in %s: hearth init --move-login %s", shortHost(sug.Machine), sug.Path, sug.Path))
		}
		return home
	}
	ans, ok := askLine(fmt.Sprintf("  %-12s %s, %s\n%15sEnter to use it, or type another directory: ", "workspace", def, why, ""))
	if !ok {
		return home
	}
	choice := def
	if ans != "" {
		choice = client.ExpandUserDir(ans)
	}
	if filepath.Clean(choice) == filepath.Clean(h) {
		out.line("workspace", "your home (state in ~/.hearth)")
		return home
	}
	nh, err := client.MoveHome(home, choice)
	if err != nil {
		out.warn("workspace", fmt.Errorf("%s: %v; staying in your home (later: hearth init --move-login <dir>)", choice, err))
		return home
	}
	os.Setenv("HEARTH_HOME", nh)
	os.Setenv("HEARTH_WORKSPACE", choice)
	os.Unsetenv("HEARTH_WORKSPACE_FROM_PROFILE")
	msg := choice
	if dev, other := client.OtherHostHome(choice); other {
		msg += fmt.Sprintf("\nshared disk (%s logged in there): this machine's state is in %s", shortHost(orDefault(dev, "another machine")), nh)
	}
	out.line("workspace", msg)
	return nh
}

// chooseSetupAlias names this machine: --alias, else asked in a terminal
// with the generated SSH name as the default; without a terminal the
// generated name stays (nothing stored).
func chooseSetupAlias(out *setupOut, e *client.Engine, alias string) {
	def := client.MachineAlias(e.Cfg.DeviceName, factsMap())
	if alias == "" {
		if !isTTY() {
			return
		}
		a, ok := askLine(fmt.Sprintf("  %-12s %s (machine %s)\n%15sEnter to keep it, or type another: ", "name", def, e.Cfg.DeviceName, ""))
		if !ok {
			return
		}
		alias = def
		if a = strings.TrimSpace(a); a != "" {
			alias = a
		}
	}
	if err := e.API.SetDeviceAlias("self", alias); err != nil {
		out.warn("name", fmt.Errorf("%v; later: hearth device rename <name>", err))
		return
	}
	out.line("name", client.LabelOf(alias, e.Cfg.DeviceName))
}

// thisAlias is this machine's name: the one the user gave, else generated.
func thisAlias(e *client.Engine) string {
	if ds, err := e.API.Devices(); err == nil {
		for _, d := range ds {
			if d.ID == e.Cfg.DeviceID && d.Alias != "" {
				return d.Alias
			}
		}
	}
	return client.MachineAlias(e.Cfg.DeviceName, factsMap())
}
