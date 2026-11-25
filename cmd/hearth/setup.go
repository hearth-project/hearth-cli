package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/api"
	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/recipe"
)

// setupCmd is the one-shot "new machine" flow run by install.sh:
// log in (if needed), restore every session, start the background daemon.
func setupCmd() *cobra.Command {
	var server, email, device, caCert, invite, team string
	var installToken string
	var insecure, noDaemon, force, yes, create, switchLogin, noRegister, noImport, noApp, full, newLogin bool
	var alias string
	var continued *takenOver
	// Activate (in the root pre-run) sets HEARTH_HOME even for an implicit
	// default. Remember whether the user actually selected a home.
	implicitHome := os.Getenv("HEARTH_HOME") == "" && os.Getenv("HEARTH_WORKSPACE") == ""
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
					out.fact("account", id.Email+" (from "+id.Source+")")
				} else {
					// Nothing says which of the people known here is asking.
					picked, perr := pickIdentity(home)
					if perr != nil {
						return perr
					}
					email = picked
					out.fact("account", email)
				}
			}
			if installToken == "" {
				installToken = os.Getenv("HEARTH_INSTALL_TOKEN")
			}
			// A workspace directory another person uses too (a shared Unix
			// account): their login there is left alone, this person gets a
			// home of their own in it (<ws>/.hearth/users/<email>).
			if ws := client.WorkspaceOf(home); ws != "" && implicitHome && !switchLogin && err == nil && !strings.EqualFold(cfg.Email, email) {
				if own := client.WorkspaceHomeFor(ws, email); filepath.Clean(own) != filepath.Clean(home) {
					other := cfg.Email
					if own, err = client.InitWorkspaceHome(ws, own); err != nil {
						return err
					}
					client.LeaveHome(home)
					os.Setenv("HEARTH_HOME", own)
					os.Setenv("HEARTH_WORKSPACE", ws)
					os.Unsetenv("HEARTH_WORKSPACE_FROM_PROFILE")
					home = own
					cfg, err = client.LoadConfig(home)
					out.fact("workspace", fmt.Sprintf("%s is also used by %s; your state is kept apart in %s", ws, other, home))
				}
			}
			// Don't redirect an explicit home, a different account, or an intentional new login.
			sameLogin := err == nil && sameServer(cfg.Server, server) && strings.EqualFold(cfg.Email, email)
			// A Unix account several people share: ~/.hearth may hold another
			// person's login. It is left alone; this person's login goes into
			// a workspace of their own (an existing one, or one they choose).
			occupiedBy := ""
			if implicitHome && home == client.DefaultHome() && err == nil && !sameLogin && !switchLogin {
				occupiedBy = cfg.Email + " on " + cfg.Server
			}
			if implicitHome && home == client.DefaultHome() && !cmd.Flags().Changed("workspace") &&
				!create && !switchLogin && invite == "" && device == "" && (errors.Is(err, client.ErrNotLoggedIn) || sameLogin || occupiedBy != "") {
				var pick func([]string) (string, bool)
				if isTTY() && installToken != "" {
					pick = func(choices []string) (string, bool) {
						return out.ask("Which existing login should this installation use?",
							[]string{"Type its number. No logins will be merged or removed."}, choices, "number")
					}
				}
				m, findErr := existingSetupWorkspace(server, email, sameLogin, installToken != "", pick)
				if findErr != nil {
					return findErr
				}
				if m != nil && m.Home != home {
					if err := prepareServer(server, caCert, insecure); err != nil {
						return err
					}
					if err := verifySetupReuse(home, m.Home, m.Config, installToken); err != nil {
						return err
					}
					installToken = "" // consumed against the existing device, not a new login
					client.LeaveHome(home)
					os.Setenv("HEARTH_HOME", m.Home)
					os.Setenv("HEARTH_WORKSPACE", m.Workspace)
					os.Unsetenv("HEARTH_WORKSPACE_FROM_PROFILE")
					home, cfg, err = client.Activate(), m.Config, nil
					out.fact("workspace", "reusing "+m.Workspace+" (existing device; no new login)")
					occupiedBy = ""
				}
			}
			staging := ""
			if occupiedBy != "" {
				if !isTTY() {
					return fmt.Errorf("~/.hearth is logged in as %s (another login on this account) and is left alone; "+
						"run setup again with --workspace <your directory> to keep your login there; no device was added", occupiedBy)
				}
				if staging, err = client.NewSetupHome(); err != nil {
					return err
				}
				defer func() {
					// Still here: setup stopped before the login moved into a
					// workspace. Nothing in it is used again.
					if staging != "" {
						_ = os.RemoveAll(staging)
					}
				}()
				client.LeaveHome(home)
				os.Setenv("HEARTH_HOME", staging)
				home = staging
				cfg, err = client.LoadConfig(home)
				out.fact("login", fmt.Sprintf("~/.hearth is logged in as %s; that login is left alone, yours goes into your own directory", occupiedBy))
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
				if installToken != "" && !create && invite == "" {
					if err := prepareServer(server, caCert, insecure); err != nil {
						return err
					}
					if err := verifySetupReuse(home, home, cfg, installToken); err != nil {
						return err
					}
				}
				out.fact("login", "already signed in as "+cfg.Email)
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
				if installToken != "" && !create && invite == "" {
					// One-time install command (the Hearth app): no password.
					if nc, err := client.LoginWithInstallToken(home, server, email, installToken, device); err == nil {
						cfg, loggedIn = nc, true
						via = " with the one-time install command (no password needed)"
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
							via = " with this platform's identity (no password needed)"
						} else if st := client.StatusOf(err); st == 0 || st >= 500 {
							fmt.Fprintf(os.Stderr, "  platform identity unavailable (%v); asking for the master password\n", err)
						}
					}
				}
				if !loggedIn {
					pw, err := readPassword(fmt.Sprintf("  %-*s  ", labelWidth, "password"), create || invite != "")
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
							out.fact("account", "exists already; invite not used")
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
				out.fact("login", "signed in"+via)
				// Record the restore policy before prompting. If naming fails or is
				// interrupted, retrying setup must not restore credentials by default.
				if err := client.SetOnDemand(home, !full); err != nil {
					return err
				}
				// Its name first: a new machine with an offline machine's
				// name (or this one before its disk was reset) continues
				// that machine, workspace included.
				continued, err = nameNewMachine(out, home, alias)
				if err != nil {
					return err
				}
				// Where this person works on this machine: proposed from their
				// machines of the same kind, confirmed (or changed) by them.
				home, err = chooseSetupWorkspace(out, home, cfg, continued, staging != "")
				if err != nil {
					return err
				}
				staging = ""
				if continued != nil {
					// A surviving workspace may have kept its old restore policy.
					if err := client.SetOnDemand(home, !full); err != nil {
						return err
					}
				}
				newLogin = true
			default:
				return err
			}

			client.RememberSetupWorkspace(home, cfg)

			// Shared account: this person's Codex config and conversations
			// in their own dir, not in the ~/.codex everybody falls back to.
			if dir := client.PreferOwnCodexHome(home); dir != "" {
				out.line("codex home", dir+" (yours; the shared ~/.codex is left alone)")
			}
			e, err := engine()
			if err != nil {
				return err
			}
			// A new name for a machine that was logged in already.
			if !newLogin && alias != "" {
				if _, err := chooseSetupAlias(out, e, alias); err != nil {
					return err
				}
			}
			var libErr, restoreErr error
			var teams string
			out.start("Setting up this machine")
			// An on-demand machine sets up no agent now (skills, skill search,
			// memory hooks and index): "恢复 <machine> 的 <agent> 环境" does,
			// through hearth agents pull. The library is pulled all the same:
			// it holds the machine profiles, backup selection and recipes.
			agentsNow := !client.OnDemand(home) || client.HasMachineAgents(home)
			out.quiet(func() {
				if !agentsNow {
					// An empty selection, not none: none means every agent.
					if err := client.SetMachineAgents(home, nil); err != nil {
						fmt.Println("agents:", err)
					}
					if r, err := e.LibPull(false); err != nil {
						libErr = err
					} else {
						fmt.Printf("library %s (version %d); agents set up when their environment is restored\n", r.Op, r.Version)
					}
					return
				}
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
							envLeft = append(envLeft, orDefault(p.Name, p.Type))
						}
					}
				}
				// After the recipes: registering the MCP server creates the
				// agent's config file (~/.codex/config.toml), which would stop
				// a recorded one from being restored.
				if len(client.MachineAgents(home)) == 0 {
					return // no agent set up here yet
				}
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
				out.line("library", fmt.Sprintf("%s and %s, linked into %s", plural(skills, "skill", "skills"), plural(agents, "agent", "agents"), strings.Join(ma, ", ")))
			} else {
				out.line("library", fmt.Sprintf("%s and %s, set up for an agent when you restore its environment", plural(skills, "skill", "skills"), plural(agents, "agent", "agents")))
			}
			if secretFiles > 0 {
				out.line("files", plural(secretFiles, "credential or config file", "credential and config files")+" written")
			}
			if len(rcs) > 0 {
				out.line("shell", "your lines of "+strings.Join(rcs, ", ")+" (new shells)")
			}
			if !onDemand || sessionsAsked {
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
				out.quiet(func() { autoRecord(e, func(f string, a ...any) { fmt.Printf(f+"\n", a...) }) })
			}
			if noDaemon {
				out.line("sync", "off (--no-daemon); start it with hearth daemon start")
			} else {
				var derr error
				out.quiet(func() { derr = startDaemon() })
				if derr != nil {
					return derr
				}
				out.line("sync", "on, in the background")
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
			// On a Mac the menu bar app comes with the command line: one setup,
			// both installed. A server that has no app to hand out is not an error.
			_, statErr := os.Stat(appDir())
			if wantMenuBarApp(runtime.GOOS, noApp, os.Getenv("HEARTH_NO_APP"), statErr == nil) {
				switch _, err := installMenuBar(e); {
				case err == nil:
					out.line("app", "Hearth.app in ~/Applications, the flame in the menu bar")
				case errors.Is(err, errAppNotServed):
					// nothing to install
				default:
					out.warn("app", fmt.Errorf("%v (retry: hearth app install)", err))
				}
			}
			var withSkill []string
			out.quiet(func() {
				var err error
				if withSkill, err = installSetupSkillTo(false, false); err != nil {
					fmt.Println("skills:", err)
				}
			})
			out.finish()
			printSetupSummary(out, e, envLeft, withSkill, codexTrust, onDemand && !sessionsAsked)
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
	c.Flags().StringVar(&installToken, "token", "", "one-time install token from an install command (the Hearth app, hearth machine install-command): log in without the password (works once, 30 minutes) [$HEARTH_INSTALL_TOKEN]")
	c.Flags().BoolVar(&insecure, "insecure", false, "allow plain HTTP to a non-loopback server")
	c.Flags().BoolVar(&noDaemon, "no-daemon", false, "do not start background sync")
	c.Flags().BoolVar(&force, "force", false, "overwrite local session files that differ from the server")
	c.Flags().IntVar(&setupLast, "sessions", setupLast, "restore this many most recently active conversations (0 = all; a new machine restores none unless given)")
	c.Flags().StringVar(&alias, "alias", "", "a name for this machine (default: asked in a terminal, proposing e.g. gb-node-e05-13); an offline machine's name continues that machine; hearth device rename changes it")
	c.Flags().BoolVar(&full, "full", false, "restore everything now (credentials, conversations, shell lines, agent setup) instead of on demand")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "unused: setup no longer runs recipe commands (the hearth skill goes through hearth recipe plan)")
	_ = c.Flags().MarkHidden("yes")
	c.Flags().BoolVar(&noApp, "no-app", false, "on a Mac, do not install the menu bar app [$HEARTH_NO_APP=1]")
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

// Never guess between two homes, including a duplicate in ~/.hearth from an
// older install. Without an install proof, don't unlock a discovered login
// just because the supplied email matches; let the user select its workspace.
func existingSetupWorkspace(server, email string, defaultLoggedIn, hasToken bool, pick func([]string) (string, bool)) (*client.WorkspaceLogin, error) {
	matches := client.SetupWorkspaces(server, email)
	if len(matches) == 0 {
		return nil, nil
	}
	if defaultLoggedIn {
		matches = append(matches, client.WorkspaceLogin{Home: client.DefaultHome()})
	}
	if len(matches) == 1 && hasToken {
		return &matches[0], nil
	}
	var dirs []string
	for i, m := range matches {
		label := shellQuote(m.Workspace)
		if m.Workspace == "" {
			label = "~/.hearth (current home)"
		}
		dirs = append(dirs, fmt.Sprintf("%d) %s", i+1, label))
	}
	if hasToken && pick != nil {
		answer, ok := pick(dirs)
		n, err := strconv.Atoi(strings.TrimSpace(answer))
		if ok && err == nil && n >= 1 && n <= len(matches) {
			return &matches[n-1], nil
		}
	}
	return nil, fmt.Errorf("existing login for %s: %s. Run setup from the intended workspace (or pass --workspace <dir>); no device was added", email, strings.Join(dirs, ", "))
}

// Validate the local vault before consuming a one-time credential. Use the
// installer's trust settings, not CA or proxy hints supplied by a discovered home.
func verifySetupReuse(trustHome, home string, cfg *client.Config, token string) error {
	if _, err := client.LoadVault(home); err != nil {
		return fmt.Errorf("cannot reuse login in %s: %w; no device was added", home, err)
	}
	if err := client.ReuseInstallToken(trustHome, cfg, token); err != nil {
		if client.StatusOf(err) == 401 || client.IsDeviceRemoved(err) {
			return fmt.Errorf("the login in %s is expired or revoked: %w. The install command was not used; no device was added. Check this workspace before signing in again", home, err)
		}
		return fmt.Errorf("cannot reuse login in %s: %w; no device was added", home, err)
	}
	return nil
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
func setupTeam(e *client.Engine, team string) string {
	teams, _, err := e.SyncTeamSources()
	if err != nil {
		fmt.Println("      teams:", err)
		return ""
	}
	if team == "" && len(teams) == 0 && isTTY() {
		a, ok := askSetup("Join a team?", []string{"A team shares skills and agents among its members; you are in none yet."},
			[]string{"Press ENTER to skip for now", "Or paste an invite code to join one", "Or type a name to create one"}, "skip")
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
// mustMove: the login is in a temporary home (~/.hearth holds another
// person's login), so it has to move into a directory other than the home.
func chooseSetupWorkspace(out *setupOut, home string, cfg *client.Config, prev *takenOver, mustMove bool) (string, error) {
	if client.WorkspaceOf(home) != "" || (!mustMove && os.Getenv("HEARTH_WORKSPACE") != "" && os.Getenv("HEARTH_WORKSPACE_FROM_PROFILE") == "") {
		return home, nil // chosen already (hearth init, --workspace)
	}
	h, _ := os.UserHomeDir()
	sug := client.WorkspaceSuggestion{}
	if e, err := client.NewEngine(home); err == nil {
		if ds, err := e.API.Devices(); err == nil {
			sug = client.SuggestWorkspace(ds, cfg.DeviceID, client.Facts(), client.WorkspaceUsable)
		}
	}
	def := h
	about := []string{"Hearth keeps this machine's state and your agents' homes there."}
	if mustMove {
		about = append(about, "~/.hearth holds another login on this account, so pick a directory of your own (not the home).")
	}
	if sug.Path != "" && (!mustMove || filepath.Clean(sug.Path) != filepath.Clean(h)) {
		def = sug.Path
		about = append(about, fmt.Sprintf("The directory below is where %s (%s) works.", shortHost(sug.Machine), sug.Kind))
	} else if mustMove {
		def = filepath.Join(h, setupUserName(cfg.Email))
	} else {
		about = append(about, "None of your machines is like this one; the directory below is your home.")
	}
	if sug.Missing != "" {
		// The most alike machine works on a disk this one does not have.
		about = append(about, fmt.Sprintf("%s works in %s, which this machine does not have.", shortHost(sug.MissingMachine), sug.Missing))
	}
	replacedID := ""
	if prev != nil {
		replacedID = prev.old.ID
		// The machine this one continues worked there: same place again,
		// without asking.
		if ws := client.WorkspaceOfDevice(prev.old); ws != "" && client.WorkspaceUsable(ws) && (!mustMove || filepath.Clean(ws) != filepath.Clean(h)) {
			return moveSetupHome(out, home, h, ws, replacedID)
		}
	}
	if !isTTY() {
		if mustMove {
			return "", errors.New("~/.hearth holds another login: run setup again with --workspace <your directory>")
		}
		if sug.Path != "" {
			out.line("workspace", fmt.Sprintf("your home for now; %s works in %s: hearth init --move-login %s", shortHost(sug.Machine), sug.Path, sug.Path))
		}
		return home, nil
	}
	choice := ""
	for choice == "" {
		ans, ok := out.ask("Where do you work on this machine?", about,
			[]string{"Press ENTER to use the directory below", "Or type another directory"}, def)
		if !ok {
			if mustMove {
				return "", errors.New("no directory chosen: run setup again (or pass --workspace <your directory>)")
			}
			return home, nil
		}
		choice = def
		if ans != "" {
			choice = client.ExpandUserDir(ans)
		}
		if mustMove && filepath.Clean(choice) == filepath.Clean(h) {
			about = []string{"Your home keeps its state in ~/.hearth, which holds another login here. Type another directory."}
			choice = ""
		}
	}
	return moveSetupHome(out, home, h, choice, replacedID)
}

// setupUserName is the directory name proposed for a person's own
// workspace on a shared account: the local part of their email.
func setupUserName(email string) string {
	name, _, _ := strings.Cut(email, "@")
	name = strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '_' || r == '-' {
			return r
		}
		return '-'
	}, name)
	if name == "" || name == "." || name == ".." {
		return "hearth-workspace"
	}
	return name
}

// moveSetupHome moves the new login from home (~/.hearth) into workspace
// choice; a home the continued device (replacedID) left there is kept.
func moveSetupHome(out *setupOut, home, h, choice, replacedID string) (string, error) {
	if filepath.Clean(choice) == filepath.Clean(h) {
		out.line("workspace", "your home (state in ~/.hearth)")
		return home, nil
	}
	nh, kept, err := client.MoveHomeOver(home, choice, replacedID)
	if err != nil {
		// Do not launch a second daemon in ~/.hearth after failing to use
		// the selected workspace (for example a surviving, unindexed login).
		return "", fmt.Errorf("could not use workspace %s: %w; setup stopped before starting background sync. Check the existing workspace with hearth --workspace %s status", choice, err, shellQuote(choice))
	}
	os.Setenv("HEARTH_HOME", nh)
	os.Setenv("HEARTH_WORKSPACE", choice)
	os.Unsetenv("HEARTH_WORKSPACE_FROM_PROFILE")
	msg := choice
	if kept {
		msg += "\nthe old machine's Hearth home is still there (" + nh + "): kept, with its agent homes and state"
	} else if dev, other := client.OtherHostHome(choice); other {
		msg += fmt.Sprintf("\nshared disk (%s logged in there): this machine's state is in %s", shortHost(orDefault(dev, "another machine")), nh)
	}
	out.line("workspace", msg)
	return nh, nil
}

// takenOver is the offline machine this one continues, and the name this
// one got from it ("" = the generated one).
type takenOver struct {
	old   api.Device
	alias string
}

// nameNewMachine names a machine that just logged in, before its
// workspace is chosen. A name stands for a machine: when an offline
// machine went by it, this one continues that machine (its conversations,
// and its workspace and Hearth home there when they survived) and the old
// entry leaves the list. This machine before its disk was reset (same
// host name, offline) is continued without asking; otherwise the name is
// --alias, or asked in a terminal with the generated name as default.
func nameNewMachine(out *setupOut, home, alias string) (*takenOver, error) {
	e, err := client.NewEngine(home)
	if err != nil {
		return nil, err
	}
	if alias == "" {
		if ds, err := e.API.Devices(); err == nil {
			if prev, ok := client.PreviousSelf(ds, e.Cfg.DeviceID, e.Cfg.DeviceName); ok {
				a, err := e.TakeOver(prev)
				if err == nil {
					t := &takenOver{old: prev, alias: a}
					out.line("name", continuedLine(e, t))
					return t, nil
				}
				out.warn("name", err)
			}
		}
	}
	return chooseSetupAlias(out, e, alias)
}

// continuedLine: "h800-07 (host), continues the machine last seen ...".
func continuedLine(e *client.Engine, t *takenOver) string {
	name := client.LabelOf(orDefault(t.alias, client.MachineAlias(e.Cfg.DeviceName, factsMap())), e.Cfg.DeviceName)
	what := "the machine of that name"
	if strings.EqualFold(t.old.Name, e.Cfg.DeviceName) {
		what = "this machine as it was"
	}
	return fmt.Sprintf("%s, continues %s (last seen %s): its conversations and workspace", name, what, agoUnix(t.old.LastSeen))
}

// chooseSetupAlias names this machine: --alias, else asked in a terminal
// with the generated SSH name as the default; without a terminal the
// generated name stays (nothing stored). An offline machine's name
// continues that machine (returned).
func chooseSetupAlias(out *setupOut, e *client.Engine, alias string) (*takenOver, error) {
	var old *api.Device
	var err error
	if alias != "" {
		old, err = e.NameMachine(e.Cfg.DeviceID, alias)
	} else if isTTY() {
		alias, old, err = promptSetupAlias(e, client.MachineAlias(e.Cfg.DeviceName, factsMap()), func(about []string, def string) (string, bool) {
			return out.ask("What should this machine be called?", about,
				[]string{"Press ENTER to keep the name below", "Or type another"}, def)
		})
	} else {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("could not name this machine: %w; finish setup with hearth setup --alias <unused-name>", err)
	}
	if old != nil {
		t := &takenOver{old: *old, alias: alias}
		out.line("name", continuedLine(e, t))
		return t, nil
	}
	out.line("name", client.LabelOf(alias, e.Cfg.DeviceName))
	return nil, nil
}

// A suggested SSH name is for a new machine; only a name the user types
// should continue an unrelated offline machine. Try readable numeric suffixes
// on a collision and leave room for them within the server's 63-byte limit.
func setupAliasDefault(base string, ds []api.Device, self string, rejected map[string]bool) string {
	used := map[string]bool{}
	for _, d := range ds {
		if d.ID == self || (d.Status != "" && d.Status != "active") {
			continue
		}
		used[strings.ToLower(client.NameOf(d))] = true
		if d.Alias == "" {
			used[strings.ToLower(d.Name)] = true
		}
	}
	base = strings.Trim(base, "-") // MachineAlias generates ASCII letters, digits and '-'
	for n := 1; ; n++ {
		suffix := ""
		if n > 1 {
			suffix = fmt.Sprintf("-%d", n)
		}
		a := base[:min(len(base), 63-len(suffix))] + suffix
		if !used[a] && !rejected[a] {
			return a
		}
	}
}

func promptSetupAlias(e *client.Engine, base string, ask func([]string, string) (string, bool)) (string, *api.Device, error) {
	about := []string{"The name is shown in your machine list and is its SSH host name.",
		"Give it the name of an offline machine to continue that one (its conversations and workspace)."}
	rejected := map[string]bool{}
	for {
		ds, err := e.API.Devices()
		if err != nil {
			return "", nil, err
		}
		def := setupAliasDefault(base, ds, e.Cfg.DeviceID, rejected)
		a, ok := ask(about, def)
		if !ok {
			return "", nil, errors.New("no name confirmed")
		}
		alias := strings.TrimSpace(a)
		var old *api.Device
		if alias == "" {
			alias = def
			// The user may have left the prompt open for a while. Include
			// generated SSH names only the client knows in the fresh check.
			ds, err = e.API.Devices()
			if err != nil {
				return "", nil, err
			}
			if setupAliasDefault(base, ds, e.Cfg.DeviceID, rejected) != def {
				about = []string{"Your machine list changed. Choose the suggested name or type another."}
				continue
			}
			err = e.API.SetUnusedDeviceAlias(e.Cfg.DeviceID, alias)
			var he *client.HTTPError
			if errors.As(err, &he) && he.Status == 400 && he.Body.Error == "invalid JSON body" {
				// Servers before only_if_unused reject the field. The original
				// generated name also works without any server-side write there.
				if alias == base {
					return alias, nil, nil
				}
				return "", nil, errors.New("server cannot reserve the suggested name; update the server or pass an unused --alias explicitly")
			}
		} else {
			old, err = e.NameMachine(e.Cfg.DeviceID, alias)
		}
		switch client.StatusOf(err) {
		case 400:
			about = []string{"Use a name starting with a letter or digit; up to 63 letters, digits, '.', '_' or '-'."}
		case 409:
			about = []string{alias + " is already in use. Choose the suggested name or type another."}
		default:
			return alias, old, err
		}
		rejected[strings.ToLower(alias)] = true // also avoid stale server listings on a retry
	}
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
