package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/hearth-project/hearth-cli/internal/client"
	"github.com/hearth-project/hearth-cli/internal/library"
	"github.com/hearth-project/hearth-cli/internal/recipe"
)

func statusCmd() *cobra.Command {
	var asJSON bool
	c := &cobra.Command{
		Use:   "status",
		Short: "Everything at a glance: login, background sync, storage, agents, library and what to do next",
		RunE: func(*cobra.Command, []string) error {
			if asJSON {
				st := collectOnboard()
				out := map[string]any{"onboard": st, "daemon": client.LoadDaemonStatus(client.Home())}
				b, _ := json.MarshalIndent(out, "", "  ")
				fmt.Println(string(b))
				return nil
			}
			return printStatus()
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "machine-readable (onboard status plus the daemon heartbeat)")
	return c
}

// printStatus is the one screen a user needs: is it working, what is
// stored, and the next steps (the same ones the hearth skill follows).
func printStatus() error {
	home := client.Home()
	st := collectOnboard()
	auto := "on"
	if !client.AutoUpdateEnabled(home) {
		auto = "off"
	}
	head := fmt.Sprintf("Hearth %s (auto-update %s)", version, auto)
	if cfg, err := client.LoadConfig(home); err == nil {
		if sv := client.ServerBuild(home, cfg.Server); sv != "" && sv != version && !strings.HasSuffix(version, "-dev") {
			head += fmt.Sprintf(" · server has %s: hearth self-update", sv)
		}
	}
	fmt.Println(head)
	t := &table{}
	if ws := st.Workspace; ws != "" {
		t.add("workspace", ws)
	}
	if !st.LoggedIn {
		t.add("account", "not logged in on this machine")
		t.print()
		printNext(st.Next)
		return nil
	}
	reach := "reachable"
	if st.ServerReachable == nil || !*st.ServerReachable {
		reach = "NOT reachable"
	}
	t.add("account", fmt.Sprintf("%s on %s (%s)", st.Email, st.Server, reach))
	if cfg, err := client.LoadConfig(home); err == nil {
		exp := time.Unix(cfg.ExpiresAt, 0)
		left := time.Until(exp)
		valid := fmt.Sprintf("login valid until %s", exp.Format("2006-01-02"))
		if left < 0 {
			valid = "login EXPIRED (it renews itself while the server is reachable; else: hearth setup)"
		} else if left < 72*time.Hour {
			valid += " (" + humanize.Time(exp) + ")"
		}
		t.add("device", cfg.DeviceName+" · "+valid)
	}
	t.add("sync", syncLine(home))
	if e, err := engine(); err == nil {
		if on := e.OnlineDevices(); len(on) > 0 {
			var names []string
			for _, n := range on {
				names = append(names, shortHost(n))
			}
			sort.Strings(names)
			t.add("online", fmt.Sprintf("%d other machine(s): %s (their conversations stay there on hearth restore)", len(names), strings.Join(names, ", ")))
		} else {
			t.add("online", "no other machine online")
		}
	}
	if ss := client.SSHServerStatus(home); ss.On || ss.Running {
		line := "on, starting with the daemon (hearth ssh status)"
		switch {
		case ss.Running:
			line = fmt.Sprintf("this machine accepts your devices at %s:%d (%d key(s)); your machines: hearth ssh", ss.Address, ss.Port, ss.Keys)
		case ss.Err != "":
			line = "not running: " + truncW(ss.Err, termWidth()-30)
		}
		t.add("ssh", line)
	}
	if hb := client.LoadDaemonStatus(home); hb.LastError != "" && st.DaemonRunning {
		t.add("", "last error "+agoUnix(hb.ErrorAt)+": "+truncW(hb.LastError, termWidth()-30))
	}
	e, err := engine()
	if err == nil {
		if u, err := e.API.Usage(); err == nil {
			pct := ""
			if u.Quota > 0 {
				pct = fmt.Sprintf(" (%.0f%%)", 100*float64(u.Used)/float64(u.Quota))
			}
			t.add("storage", fmt.Sprintf("%s of %s%s · sessions %s · library %s · secrets %s", bytesString(u.Used), quotaString(u.Quota), pct,
				bytesString(u.SessionBytes), bytesString(u.LibraryBytes), bytesString(u.SecretBytes)))
		}
		first := true
		for _, a := range e.Adapters {
			label := "agents"
			if !first {
				label = ""
			}
			fs, err := a.Sessions()
			var line string
			switch {
			case err != nil:
				line = fmt.Sprintf("%-7s error: %v", a.Name(), err)
			case client.SupportsSessions(a.Name()):
				line = fmt.Sprintf("%-7s %d local session file(s) in %s", a.Name(), len(fs), a.Root())
				if s, ok := st.Agents[a.Name()]; ok && s.MCPRegistered {
					line += " · skill search on"
				}
			default:
				continue // backup sources: listed under backup
			}
			first = false
			t.add(label, line)
		}
		for _, a := range []string{"codex", "claude", "comate"} {
			if s, ok := st.Agents[a]; ok && !s.Enabled && s.Installed {
				t.add("", fmt.Sprintf("%-7s installed but not synced here (hearth agents pull %s)", a, a))
			}
		}
	}
	if e != nil {
		if teams, err := e.API.Teams(); err == nil {
			if len(teams) == 0 {
				t.add("teams", "none (create: hearth team create <name> · join: hearth team join <invite code>)")
			} else {
				var names []string
				for _, tm := range teams {
					n := tm.Name + " (" + tm.Role
					if tm.Via != "" {
						n += " via " + tm.Via
					}
					names = append(names, n+")")
				}
				t.add("teams", strings.Join(names, ", "))
			}
		}
	}
	if lc, err := (library.Library{Root: client.LibraryDir(home)}).Load(); err == nil {
		line := fmt.Sprintf("%d skills, %d agents", len(lc.Skills), len(lc.Agents))
		if lc.Resident != nil {
			line += fmt.Sprintf(" · resident: %s", truncW(strings.Join(lc.Resident, ", "), termWidth()-40))
		}
		t.add("library", line)
	}
	t.print()
	printNext(st.Next)
	return nil
}

// syncLine describes the background daemon from its heartbeat.
func syncLine(home string) string {
	pid := client.DaemonPID(home)
	if pid == 0 {
		return "background sync NOT running (start: hearth daemon start)"
	}
	hb := client.LoadDaemonStatus(home)
	line := "running"
	if pid > 0 {
		line += fmt.Sprintf(" (pid %d)", pid)
	}
	if hb.LastScan > 0 {
		line += " · checked " + agoUnix(hb.LastScan)
		if time.Since(time.Unix(hb.LastScan, 0)) > 10*time.Minute {
			line += " (stalled? see " + client.DaemonLog(home) + ")"
		}
	}
	if hb.LastUpload > 0 {
		line += fmt.Sprintf(" · last upload %s (%s)", agoUnix(hb.LastUpload), humanize.IBytes(uint64(hb.UploadBytes)))
	}
	if hb.Version != "" && hb.Version != version {
		line += " · runs " + hb.Version + " (restart: hearth daemon stop && hearth daemon start)"
	}
	return line
}

func printNext(next []nextStep) {
	if len(next) == 0 {
		fmt.Println("\nall set")
		return
	}
	fmt.Println("\nnext:")
	for i, n := range next {
		who := ""
		if n.UserTerminal {
			who = ""
		}
		cmd := n.Command
		switch n.ID { // the agent-facing commands have interactive forms
		case "learn":
			cmd = "hearth learn   # one checklist, Enter keeps the proposals; not now: hearth learn --later"
		case "recipes":
			cmd = "ask your agent \"用 hearth 恢复这台机器的环境\"   # or yourself: hearth recipe plan, then hearth recipe done <key>"
		}
		fmt.Printf("  %d. %s%s\n     %s\n", i+1, n.Why, who, cmd)
	}
}

func pushCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "push",
		Aliases: []string{"flush"},
		Short:   "Upload all new session data once (use as k8s preStop hook)",
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			st, err := e.PushOnce()
			fmt.Printf("%d files scanned, %d updated, %d skipped, %d bytes uploaded\n", st.Files, st.Uploaded, st.Skipped, st.Bytes)
			if st.Foreign > 0 {
				fmt.Printf("%d session file(s) of other people or outside your workspace not uploaded (hearth session scope)\n", st.Foreign)
			}
			e.ReleaseLeases()
			return err
		},
	}
}

func daemonCmd() *cobra.Command {
	var interval, libInterval, updateInterval time.Duration
	c := &cobra.Command{
		Use:   "daemon",
		Short: "Continuously sync session files in the foreground (flushes on SIGTERM)",
		RunE: func(*cobra.Command, []string) error {
			client.EnsureUserPath() // tools the user installed after whatever started the daemon
			e, err := engine()
			if err != nil {
				return err
			}
			lock, err := client.LockDaemon(e.Home)
			if err != nil {
				return err
			}
			defer lock.Close()
			if created, err := e.EnsureBackupConfig(); err != nil {
				log.Printf("backup selection: %v", err)
			} else if created {
				log.Printf("created backup selection %s (hearth backup status)", client.BackupConfigPath(e.Home))
			}
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			log.Printf("hearth daemon: syncing every %s to %s", interval, e.Cfg.Server)
			if err := client.AutoEnableIdentityLogin(e.Home); err != nil {
				log.Printf("identity login: %v", err)
			}
			var lastLib, lastUsage, lastMemory time.Time
			var lastLibMsg, lastMemoryMsg string
			// This device's SSH key, so the user's machines accept it.
			if _, err := e.PublishDeviceKey(); err != nil {
				log.Printf("ssh key: %v", err)
			}
			var lastSSH string
			skillTargetsSeen := fmt.Sprint(len(skillTargets()), skillTargets()) // setup installed it for these
			var lastHealth time.Time
			proxySaved := false
			var health client.HealthReport
			// Memory hooks follow the setting (and a moved binary).
			if res, err := client.SyncMemoryHooks(e.Home, client.MemoryHooksOn(e.Home)); err == nil {
				for _, r := range res {
					if r.Op == "installed" || r.Op == "removed" {
						log.Printf("memory hooks %s: %s %s", r.Agent, r.Op, r.Path)
					}
				}
			}
			shareSeen := map[string]string{} // team/type/name -> last logged op
			hb := client.DaemonStatus{PID: os.Getpid(), Version: version, Started: time.Now().Unix(), Interval: interval.String()}
			// First update check a few minutes after start, then every
			// updateInterval (short-lived daemons still update).
			firstCheck := 5 * time.Minute
			if updateInterval < firstCheck {
				firstCheck = updateInterval
			}
			lastUpdate := time.Now().Add(firstCheck - updateInterval)
			// Resolved now: once the file is replaced, /proc/self/exe names
			// the deleted old build.
			exe, err := selfExe()
			if err != nil {
				log.Printf("self-update disabled: %v", err)
				exe = ""
			}
			t := time.NewTicker(interval)
			defer t.Stop()
			for {
				if time.Since(lastUsage) >= time.Hour {
					lastUsage = time.Now()
					for _, team := range client.AutoShareTeams(e.Home) {
						if _, err := e.ShareUsage(team); err != nil {
							log.Printf("share usage with %s: %v", team, err)
						}
					}
					if _, err := e.UploadUsage(); err != nil {
						log.Printf("upload usage: %v", err)
					}
				}
				st, err := e.PushOnce()
				// sshd for the user's devices: running, current keys, endpoint published.
				{
					ss, serr := e.EnsureSSHD()
					msg := ""
					switch {
					case serr != nil:
						msg = "ssh: " + serr.Error()
					case ss.Running:
						msg = fmt.Sprintf("ssh: accepting %d key(s) at %s:%d", ss.Keys, ss.Address, ss.Port)
					}
					if msg != lastSSH && msg != "" {
						log.Print(msg)
					}
					lastSSH = msg
				}
				if time.Since(lastMemory) >= memoryInterval {
					lastMemory = time.Now()
					ms, merr := e.IndexMemory()
					msg := ""
					if merr != nil {
						msg = "memory index: " + merr.Error()
					} else if ms.Updated+ms.Removed > 0 && ms.Updated > 20 {
						msg = fmt.Sprintf("memory index: %d updated, %d removed (%s)", ms.Updated, ms.Removed, ms.Took.Round(time.Millisecond))
					}
					if msg != "" && msg != lastMemoryMsg {
						log.Print(msg)
					}
					lastMemoryMsg = msg
					if _, _, err := writeSSHConfig(e, false); err != nil {
						log.Printf("ssh config: %v", err)
					}
				}
				// Publish this machine's architecture and agent readiness so another
				// logged-in machine can choose the right restore flow.
				if time.Since(lastHealth) >= 5*time.Minute || lastHealth.IsZero() {
					health = e.MachineHealth()
					lastHealth = time.Now()
				}
				h := health
				if err := e.API.HeartbeatReport(map[string]any{"facts": h.Machine, "profiles": h.Profiles, "workspace": h.Workspace}, map[string]any{"status": h.Status, "client": h.Client, "agents": h.Agents, "tools": h.Tools, "recipe_todo": h.RecipeTodo, "recipe_manual": h.RecipeManual, "todo_steps": h.TodoSteps, "issues": h.Issues, "checked_at": h.CheckedAt}); err != nil {
					log.Printf("health heartbeat: %v", err)
				} else if !proxySaved {
					proxySaved = true
					client.RememberProxy(e.Home) // reached the server: commands from shells without the proxy reuse it
				}
				// Online for the other machines: their default restore leaves
				// out the conversations this machine is working on.
				if n, err := e.API.HeartbeatIntents(); err == nil && n > 0 {
					// Changes asked for in the web console.
					if lines, err := e.ApplyIntents(); err != nil {
						log.Printf("console changes: %v", err)
					} else {
						for _, l := range lines {
							log.Printf("console: %s", l)
						}
					}
				}
				hb.LastScan = time.Now().Unix()
				if err != nil {
					log.Printf("push: %v", err)
					hb.Fail("upload", err)
				} else if strings.HasPrefix(hb.LastError, "upload: ") {
					hb.LastError, hb.ErrorAt = "", 0
				}
				if st.Bytes > 0 {
					log.Printf("uploaded %d bytes from %d files", st.Bytes, st.Uploaded)
					hb.LastUpload, hb.UploadBytes, hb.UploadFiles = hb.LastScan, st.Bytes, st.Uploaded
				}
				if time.Since(lastLib) >= libInterval {
					lastLib = time.Now()
					// An agent installed after setup (e.g. Codex during the
					// environment restore) gets the hearth skill too.
					if ts := skillTargets(); fmt.Sprint(len(ts), ts) != skillTargetsSeen {
						skillTargetsSeen = fmt.Sprint(len(ts), ts)
						if got, err := installSetupSkillTo(false, false); err != nil {
							log.Printf("hearth skill: %v", err)
						} else if len(got) > 0 {
							log.Printf("hearth skill installed for %s", strings.Join(got, ", "))
						}
					}
					if r, err := e.SiteSync(); err != nil {
						log.Printf("site defaults: %v", err)
					} else if r.Op == "pulled" || r.Op == "removed" {
						log.Printf("site defaults %s", r.Op)
					}
					// Teams joined or left elsewhere become (or stop being)
					// skill sources; the library push below carries it.
					_, _, _ = e.SyncTeamSources()
					r, err := e.LibAuto()
					hb.LastLibrary, hb.LibraryOp = time.Now().Unix(), r.Op
					if err != nil {
						hb.Fail("library", err)
					} else if strings.HasPrefix(hb.LastError, "library: ") {
						hb.LastError, hb.ErrorAt = "", 0
					}
					if msg := fmt.Sprint(r.Op, err); err != nil && msg != lastLibMsg {
						log.Printf("library: %v", err)
						lastLibMsg = msg
					} else if err == nil && (r.Op == "pushed" || r.Op == "pulled") {
						log.Printf("library %s (version %d)", r.Op, r.Version)
						lastLibMsg = ""
					}
					// Each team's most used skills and agents are kept in every
					// agent here, so they get invoked like the user's own.
					if err == nil {
						logTeamTop(e, shareSeen)
						// What a new machine needs, recorded without hearth learn.
						daemonRecord(e, shareSeen)
					}
					// The most used skills and agents follow to the user's teams.
					if res, err := e.ShareTop(false); err != nil {
						if msg := "share-top: " + err.Error(); shareSeen[""] != msg {
							log.Print(msg)
							shareSeen[""] = msg
						}
					} else {
						for _, r := range res {
							k := r.Team + "/" + r.Type + "/" + r.Name
							if r.Op != "unchanged" && shareSeen[k] != r.Op+r.Detail {
								log.Printf("team %s: %s %s %s v%d %s", r.Team, r.Op, r.Type, r.Name, r.Version, r.Detail)
							}
							shareSeen[k] = r.Op + r.Detail
						}
					}
					// Due capture steps (already approved on this machine) keep
					// exported state such as login tokens fresh.
					if res, err := e.RunRecipes(nil, recipe.Options{Mode: recipe.Save, OnlyDue: true}); err == nil {
						for _, x := range res {
							if x.Op == "applied" || x.Op == "failed" {
								log.Printf("capture %s %s: %s %s", x.Recipe, x.Step, x.Op, x.Detail)
							}
						}
					}
				}
				_ = client.SaveDaemonStatus(e.Home, hb)
				if updateInterval > 0 && time.Since(lastUpdate) >= updateInterval {
					lastUpdate = time.Now()
					if exe != "" && daemonUpdate(e, exe) {
						if st, err := e.PushOnce(); err != nil {
							log.Printf("final push: %v", err)
						} else if st.Bytes > 0 {
							log.Printf("uploaded %d bytes before restarting", st.Bytes)
						}
						e.ReleaseLeases()
						lock.Close()
						// Same pid, args and environment: the new build takes over.
						err := syscall.Exec(exe, os.Args, os.Environ())
						log.Printf("restart on the new build failed: %v; exiting (start it again: hearth daemon start)", err)
						return err
					}
				}
				select {
				case <-ctx.Done():
					// Final flush so a graceful stop loses nothing, then let
					// other machines take the sessions over immediately.
					if st, err := e.PushOnce(); err != nil {
						log.Printf("final push: %v", err)
					} else {
						log.Printf("final flush: %d bytes", st.Bytes)
					}
					e.ReleaseLeases()
					_ = e.API.SignOff()
					return nil
				case <-t.C:
				}
			}
		},
	}
	// Defaults favour a light footprint on long-running machines; a graceful
	// stop (SIGTERM, `hearth flush`) still uploads everything immediately.
	c.Flags().DurationVar(&interval, "interval", 60*time.Second, "session scan interval")
	c.Flags().DurationVar(&libInterval, "lib-interval", 20*time.Minute, "library sync interval")
	c.Flags().DurationVar(&updateInterval, "update-interval", defaultUpdateInterval, "how often to install a new hearth build from the server (0 = never; hearth self-update --auto off)")
	c.AddCommand(&cobra.Command{
		Use: "start", Short: "Start the daemon in the background",
		RunE: func(*cobra.Command, []string) error {
			if _, err := client.LoadConfig(client.Home()); err != nil {
				return err
			}
			return startDaemon()
		},
	}, &cobra.Command{
		Use: "stop", Short: "Stop the background daemon (it flushes first)",
		RunE: func(*cobra.Command, []string) error {
			if err := client.StopDaemon(client.Home(), 30*time.Second); err != nil {
				return err
			}
			fmt.Println("daemon stopped")
			return nil
		},
	}, &cobra.Command{
		Use: "status", Short: "Show whether the daemon is running",
		RunE: func(*cobra.Command, []string) error {
			switch pid := client.DaemonPID(client.Home()); {
			case pid == 0:
				fmt.Println("daemon: not running")
			case pid < 0:
				fmt.Println("daemon: running (pid unknown)")
			default:
				fmt.Printf("daemon: running (pid %d), log %s\n", pid, client.DaemonLog(client.Home()))
			}
			return nil
		},
	})
	return c
}

func startDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	pid, err := client.StartDaemon(client.Home(), exe)
	if errors.Is(err, client.ErrDaemonRunning) {
		fmt.Printf("daemon already running (pid %d)\n", pid)
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Printf("daemon started (pid %d), log %s\n", pid, client.DaemonLog(client.Home()))
	return nil
}

// defaultRestoreLast is how many conversations restore and setup bring
// back unless asked for more: the newest are what people continue.
const defaultRestoreLast = 10

func restoreCmd() *cobra.Command {
	var agent string
	var force, all, pick, yes bool
	last := defaultRestoreLast
	c := &cobra.Command{
		Use:   "restore [session-id ...]",
		Short: "Bring conversations back from other machines (pick from a list, or the newest N)",
		Long: "Restores the newest conversations (by last activity, newest first; default " +
			fmt.Sprint(defaultRestoreLast) + ") and every memory note.\n" +
			"Use --last N for more, --all for everything, `hearth session list` to see them all,\n" +
			"or pass one or more ids (any unique prefix) to restore just those.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			if all {
				last = 0
			}
			// In a terminal with nothing chosen, offer the list first.
			if pick || (len(args) == 0 && !all && !yes && !cmd.Flags().Changed("last") && isTTY()) {
				keys, ok, err := pickConversations(e, agent, last)
				if err != nil || !ok {
					return err
				}
				if keys != nil {
					return restoreAll(e, agent, force, 0, keys)
				}
			}
			if len(args) > 0 {
				only, err := restoreKeys(e, args)
				if err != nil {
					return err
				}
				return restoreAll(e, agent, force, 0, only)
			}
			return restoreAll(e, agent, force, last, nil)
		},
	}
	c.Flags().StringVar(&agent, "agent", "", "only restore sessions of this agent (codex, claude)")
	c.Flags().BoolVar(&force, "force", false, "overwrite local files that differ from the server copy")
	c.Flags().IntVarP(&last, "last", "n", last, "restore the N most recently active conversations")
	c.Flags().BoolVar(&all, "all", false, "restore every conversation")
	c.Flags().BoolVarP(&pick, "pick", "i", false, "choose the conversations from a numbered list (default in a terminal)")
	c.Flags().BoolVarP(&yes, "yes", "y", false, "do not ask: restore the newest N right away")
	c.Flags().BoolVar(&restoreIncludeOnline, "include-online", false, "also restore conversations last written on machines that are still online (left out by default)")
	return c
}

// pickConversations shows the newest conversations numbered and reads the
// choice. keys nil with ok means "the default newest N"; ok false: cancelled.
func pickConversations(e *client.Engine, agent string, last int) (map[string]bool, bool, error) {
	all, err := e.List()
	if err != nil {
		return nil, false, err
	}
	allowed := map[string]bool{}
	for _, a := range client.MachineAgents(e.Home) {
		allowed[a] = true
	}
	var rows []conversationRow
	for _, r := range listConversations(all, listOpts{agent: agent}) {
		if r.Status == "expired" || (agent == "" && !allowed[r.Meta.Agent]) {
			continue
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		fmt.Println("no conversations on the server to restore (other agents: hearth restore --agent <name>)")
		return nil, false, nil
	}
	shown := rows
	if len(shown) > 20 {
		shown = shown[:20]
	}
	online := map[string]string{}
	if !restoreIncludeOnline {
		online = e.OnlineDevices()
	}
	printConversations(shown, true, e.Cfg.DeviceID, online)
	if len(online) > 0 {
		fmt.Println("● = machine still online: Enter leaves its conversations there; pick them by number to take them anyway")
	}
	if len(rows) > len(shown) {
		fmt.Printf("(%d more: hearth session list -s <text>, then hearth restore <id>)\n", len(rows)-len(shown))
	}
	free := 0
	for _, r := range rows {
		if _, ok := online[r.UpdatedBy]; !ok {
			free++
		}
	}
	def := last
	if def <= 0 || def > free {
		def = free
	}
	fmt.Printf("Restore which? e.g. 1 3 5-7 · Enter = newest %d · a = all · q = cancel: ", def)
	line, _ := stdinLines.ReadString('\n')
	idx, mode, err := parsePick(line, len(shown))
	if err != nil {
		return nil, false, err
	}
	switch mode {
	case "cancel":
		fmt.Println("nothing restored")
		return nil, false, nil
	case "default":
		return nil, true, nil
	case "all":
		idx = nil
		for i := range rows {
			idx = append(idx, i)
		}
	}
	keys := map[string]bool{}
	for _, i := range idx {
		keys[rows[i].Key] = true
		for _, k := range rows[i].Subagents {
			keys[k] = true
		}
	}
	return keys, true, nil
}

// parsePick reads "1 3 5-7" (1-based, up to n), "" (default), "a" or "q".
func parsePick(line string, n int) ([]int, string, error) {
	line = strings.TrimSpace(strings.ToLower(line))
	switch line {
	case "":
		return nil, "default", nil
	case "q", "quit", "n", "no":
		return nil, "cancel", nil
	case "a", "all":
		return nil, "all", nil
	}
	seen := map[int]bool{}
	var out []int
	for _, f := range strings.FieldsFunc(line, func(r rune) bool { return r == ' ' || r == ',' || r == '，' }) {
		lo, hi := f, f
		if i := strings.IndexByte(f, '-'); i > 0 {
			lo, hi = f[:i], f[i+1:]
		}
		a, err1 := strconv.Atoi(lo)
		b, err2 := strconv.Atoi(hi)
		if err1 != nil || err2 != nil || a < 1 || b > n || a > b {
			return nil, "", fmt.Errorf("not a choice: %q (numbers 1-%d, ranges like 2-4)", f, n)
		}
		for i := a; i <= b; i++ {
			if !seen[i] {
				seen[i] = true
				out = append(out, i-1)
			}
		}
	}
	return out, "pick", nil
}

// conversation reports whether a session counts against --last (memory
// notes are always restored; they are few and small).
func conversation(rs client.RemoteSession) bool { return client.IsConversation(rs) }

func short(id string) string { return id[:12] }

// restoreKeys maps session id prefixes to conversation keys, so restoring
// one rollout file of a Codex thread restores the whole conversation.
func restoreKeys(e *client.Engine, refs []string) (map[string]bool, error) {
	out := map[string]bool{}
	for _, ref := range refs {
		rs, err := e.Resolve(ref)
		if err != nil {
			return nil, err
		}
		out[client.ConversationKey(rs.Meta.Agent, rs.Meta.RelPath, rs.ID)] = true
	}
	return out, nil
}

// restoreOutcome is what the last restoreAll did, for setup's questions
// and summary.
type restoreOutcome struct {
	Written    int               // conversations new or updated here
	LeftOnline []conversationRow // left out: last written on a machine still online (newest first)
}

var lastRestore restoreOutcome

func restoreAll(e *client.Engine, agent string, force bool, last int, only map[string]bool) error {
	lastRestore = restoreOutcome{}
	all, err := e.List()
	if err != nil {
		return err
	}
	counts := map[string]int{}
	skippedWhy := map[string]int{} // reason -> files and notes skipped for it
	busy := map[string]int{}       // online machine -> conversations left there
	report := restoreReport{thisDevice: e.Cfg.DeviceName}
	defer func() { e.Progress = nil }()
	key := func(rs client.RemoteSession) string {
		return client.ConversationKey(rs.Meta.Agent, rs.Meta.RelPath, rs.ID)
	}
	// Snapshot agent UI state before writing anything (Codex: which
	// threads its sidebar already knows).
	prepared := map[string]any{}
	for _, in := range client.Integrations() {
		prepared[in.Name()] = in.Prepare(e)
	}
	var restored []client.RestoredSession
	pick := map[string]bool{}
	total, expired := 0, 0
	if only != nil {
		pick = only
	}
	// Without --agent, only the agents this machine restores (agents
	// unused for a while are pulled on request: hearth agents pull).
	allowed := map[string]bool{}
	for _, a := range client.MachineAgents(e.Home) {
		allowed[a] = true
	}
	supported := map[string]bool{}
	for _, a := range client.SupportedAgents {
		supported[a] = true
	}
	// skipAgent: sessions and memory of agents not restored here. Files of
	// the workspace / backup selection are not an agent's and always come.
	skipAgent := func(rs client.RemoteSession) bool {
		if agent != "" {
			return rs.Meta.Agent != agent
		}
		return only == nil && supported[rs.Meta.Agent] && !allowed[rs.Meta.Agent]
	}
	rows := listConversations(all, listOpts{agent: agent})
	for _, r := range rows {
		for _, k := range r.Folded {
			report.foldTo(k, r.Key) // one line per conversation in the report
		}
	}
	if only != nil {
		seenKey := map[string]bool{}
		for _, rs := range all {
			if skipAgent(rs) || !conversation(rs) || seenKey[key(rs)] {
				continue
			}
			seenKey[key(rs)] = true
			if pick[key(rs)] {
				total++
			}
		}
	} else {
		// Conversations another machine that is still online wrote last
		// stay there unless asked for (that machine is working on them).
		online := map[string]string{}
		if !restoreIncludeOnline {
			online = e.OnlineDevices()
		}
		// Newest activity first, a conversation's subagent threads and
		// Desktop copies counted with it; empty threads left out.
		for _, r := range rows {
			if skipAgent(r.RemoteSession) || !conversation(r.RemoteSession) {
				continue
			}
			if name, ok := online[r.UpdatedBy]; ok && r.Status != "expired" {
				busy[shortHost(name)]++
				lastRestore.LeftOnline = append(lastRestore.LeftOnline, r)
				continue
			}
			if r.Status == "expired" {
				// Deleted in the agent or unused for too long: kept on the
				// server only until GC; `hearth session restore <id>` revives it.
				expired++
				continue
			}
			total++
			if last <= 0 || total <= last {
				pick[r.Key] = true
				for _, k := range r.Subagents {
					pick[k] = true
				}
			}
		}
	}
	picked := total
	if only == nil && last > 0 && last < total {
		picked = last
	}
	for _, rs := range all {
		if skipAgent(rs) {
			continue
		}
		if only != nil && !conversation(rs) {
			continue
		}
		if conversation(rs) && (!pick[key(rs)] || (only == nil && rs.Status == "expired")) {
			continue
		}
		if only != nil && rs.Status == "expired" {
			_, _ = e.API.Touch(rs.ID) // restoring by id revives it
		}
		if rs.Meta.Agent == "wip" {
			continue // uncommitted-work snapshots of older versions: nothing to restore
		}
		if term.IsTerminal(int(os.Stdout.Fd())) {
			e.Progress = progressLine("downloading " + truncW(titleOf(rs), 40))
		}
		r, err := e.Restore(rs, force)
		if err != nil {
			fmt.Printf("%s  error: %v\n", short(rs.ID), err)
			counts["error"]++
			continue
		}
		counts[r.Action]++
		restored = append(restored, client.RestoredSession{Remote: rs, Result: r, Group: key(rs)})
		if r.Action == "skipped" {
			if conversation(rs) {
				fmt.Printf("%s  skipped  %s: %s\n", short(rs.ID), r.Path, r.Reason)
			} else {
				skippedWhy[r.Reason]++ // files and notes: one line per reason below
			}
		}
		if conversation(rs) {
			report.add(rs, key(rs), r.Action)
		} else if r.Action != "up-to-date" && r.Action != "skipped" {
			report.other++
		}
	}
	for why, n := range skippedWhy {
		fmt.Printf("skipped %d workspace file(s) / note(s): %s\n", n, why)
	}
	lastRestore.Written = report.print()
	switch {
	case only != nil:
	case picked < total:
		fmt.Printf("brought back the %d most recently active of %d conversations; more: hearth restore -n N (or --all, or pick: hearth restore -i)\n", picked, total)
	case total > 0:
		fmt.Printf("brought back all %d conversations\n", total)
	}
	if n := sumCounts(busy); n > 0 && !restoreAskOnline {
		fmt.Printf("left out %d conversation(s) last written on machines still online (%s); restore one by id, or all: hearth restore --include-online\n",
			n, joinCounts(busy))
	}
	if expired > 0 {
		fmt.Printf("left out %d expired conversations (deleted in the agent or unused); bring one back with hearth session restore <id>\n", expired)
	}
	opts := client.RestoreOptions{Explicit: only != nil, Say: func(f string, a ...any) { fmt.Printf(f, a...) }, Prepared: prepared}
	done := map[string]bool{}
	for _, r := range restored {
		ag := r.Remote.Meta.Agent
		if done[ag] {
			continue
		}
		done[ag] = true
		if in, ok := client.Integration(ag); ok {
			if err := in.AfterRestore(e, all, restored, opts); err != nil {
				fmt.Printf("%s integration: %v\n", ag, err)
			}
		}
	}
	if counts["error"] > 0 {
		return fmt.Errorf("%d sessions failed to restore", counts["error"])
	}
	return nil
}

// applyCodexThreads restores Codex Desktop thread names and sidebar
// sections; failures only warn (sessions are already restored).
func applyCodexThreads(e *client.Engine, all []client.RemoteSession, changed []string) {
	if len(client.CodexThreadsToApply(all)) == 0 && len(changed) == 0 {
		return
	}
	st, err := e.ApplyAgentState(all, changed...)
	if st.Named+st.Sectioned+st.Indexed > 0 || err == nil {
		fmt.Printf("codex threads: indexed=%d renamed=%d moved-to-section=%d unchanged=%d not-found=%d\n", st.Indexed, st.Named, st.Sectioned, st.Unchanged, st.Missing)
	}
	if err != nil {
		fmt.Printf("codex threads: %v (re-run later: hearth codex-threads)\n", err)
	}
}

// codexDesktopHint: a running Codex Desktop caches its sidebar list and
// does not pick up threads added behind its back until it reloads.
const codexDesktopHint = "note: an open Codex Desktop window won't list restored threads in the sidebar until it restarts; " +
	"find them now with Search (thread name or id)"

func codexThreadsCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "codex-threads",
		Short: "Make restored sessions show up in Codex Desktop (index threads, names, sidebar sections)",
		Long: "Codex rebuilds its thread list from restored rollouts but not names or sidebar sections; " +
			"setup and restore apply them through `codex app-server`. Run this if Codex was installed later.",
		RunE: func(*cobra.Command, []string) error {
			e, err := engine()
			if err != nil {
				return err
			}
			all, err := e.List()
			if err != nil {
				return err
			}
			// Every local rollout: registers threads Codex has not indexed
			// (Desktop sidebar) and refreshes their working directory.
			var local []string
			for _, a := range e.Adapters {
				if a.Name() == "codex" {
					fs, _ := a.Sessions()
					for _, f := range fs {
						local = append(local, f.AbsPath)
					}
				}
			}
			st, err := e.ApplyAgentState(all, local...)
			fmt.Printf("codex threads: indexed=%d renamed=%d moved-to-section=%d unchanged=%d not-found=%d\n", st.Indexed, st.Named, st.Sectioned, st.Unchanged, st.Missing)
			return err
		},
	}
}

func codexImportCmd() *cobra.Command {
	var dryRun bool
	c := &cobra.Command{
		Use:   "codex-import [claude-session-file | hearth-session-id ...]",
		Short: "Import Claude Code sessions into Codex (alias for `agent import codex --from claude`)",
		RunE: func(_ *cobra.Command, args []string) error {
			return runAgentImport("codex", "claude", dryRun, args)
		},
	}
	c.Flags().BoolVar(&dryRun, "dry-run", false, "only show what would be imported")
	return c
}

func selfUpdateCmd() *cobra.Command {
	var server, auto string
	c := &cobra.Command{
		Use:   "self-update",
		Short: "Replace this binary with the server's build for this platform (checksum verified)",
		Long: "The background daemon also does this by itself every few hours (after the server publishes a new build).\n" +
			"Turn that off on this machine with --auto off; a server admin can turn it off for everyone with\n" +
			"`hearth server --no-client-auto-update`.",
		RunE: func(*cobra.Command, []string) error {
			home := client.Home()
			switch auto {
			case "":
			case "on", "off":
				if err := client.SetAutoUpdate(home, auto == "on"); err != nil {
					return err
				}
				fmt.Printf("automatic updates by the daemon: %s on this machine\n", auto)
				return nil
			case "status":
				state := "on"
				if !client.AutoUpdateEnabled(home) {
					state = "off"
				}
				fmt.Printf("version %s; automatic updates by the daemon: %s (every %s)\n", version, state, defaultUpdateInterval)
				return nil
			default:
				return fmt.Errorf("--auto takes on, off or status")
			}
			if server == "" {
				cfg, err := client.LoadConfig(home)
				if err != nil {
					return fmt.Errorf("%w (or pass --server)", err)
				}
				server = cfg.Server
			}
			exe, err := selfExe()
			if err != nil {
				return err
			}
			r, err := client.SelfUpdate(home, server, exe)
			if err != nil {
				return err
			}
			if !r.Updated {
				fmt.Printf("%s is up to date (%s)\n", r.Path, describeBuild(r))
				return nil
			}
			fmt.Printf("updated %s to %s (%s)\n", r.Path, client.PlatformBinary(), describeBuild(r))
			if client.DaemonPID(home) != 0 {
				if err := client.StopDaemon(home, 30*time.Second); err != nil {
					return fmt.Errorf("restart the daemon yourself: %w", err)
				}
				if err := startDaemon(); err != nil {
					return err
				}
				fmt.Println("daemon restarted on the new binary")
			}
			// The new binary carries the current skills: refresh them.
			if out, err := refreshSkills(r.Path); err != nil {
				fmt.Printf("skills: %v\n%s", err, out)
			} else {
				fmt.Print(out)
			}
			return nil
		},
	}
	c.Flags().StringVar(&server, "server", "", "server URL (default: the one you are logged in to)")
	c.Flags().StringVar(&auto, "auto", "", "on | off | status: whether the daemon installs new builds by itself on this machine")
	return c
}

// defaultUpdateInterval is how often the daemon looks for a new build (a
// small manifest request; the binary is downloaded only when it changed).
const defaultUpdateInterval = 30 * time.Minute

func describeBuild(r client.UpdateResult) string {
	if r.Version != "" {
		return r.Version + ", " + r.SHA256[:12]
	}
	return r.SHA256[:12]
}

// selfExe is the real path of the running binary (symlinks resolved).
func selfExe() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(exe)
}

func refreshSkills(exe string) (string, error) {
	out, err := exec.Command(exe, "onboard", "install-skill", "--quiet").CombinedOutput()
	return string(out), err
}

// daemonUpdate installs a new build published on the server. It returns
// true when the binary was replaced and the daemon should re-exec.
func daemonUpdate(e *client.Engine, exe string) bool {
	r, err := client.SelfUpdateWith(e.Home, e.Cfg.Server, exe, client.UpdateOptions{Auto: true})
	if err != nil {
		log.Printf("self-update: %v", err)
		return false
	}
	if !r.Updated {
		return false
	}
	log.Printf("self-update: installed %s; restarting on it", describeBuild(r))
	if out, err := refreshSkills(exe); err != nil {
		log.Printf("skills: %v %s", err, strings.TrimSpace(out))
	}
	return true
}

// restoreReport prints one line per conversation (not per file: a Codex
// thread can be several rollout files) and a plain summary.
type restoreReport struct {
	thisDevice string
	order      []string
	rows       map[string]*reportRow
	parent     map[string]string // folded thread -> the conversation it is listed under
	other      int               // memory notes, workspace files, snapshots written
}

type reportRow struct {
	rs     client.RemoteSession
	action string
	own    bool // rs is the conversation itself, not a folded sub-thread
}

// actionRank: the most telling action of a conversation's files wins.
var actionRank = map[string]int{"up-to-date": 0, "remapped": 1, "appended": 2, "restored": 3, "skipped": 4}

func (r *restoreReport) foldTo(child, parent string) {
	if r.parent == nil {
		r.parent = map[string]string{}
	}
	r.parent[child] = parent
}

func (r *restoreReport) add(rs client.RemoteSession, key, action string) {
	if r.rows == nil {
		r.rows = map[string]*reportRow{}
	}
	own := true
	if p, ok := r.parent[key]; ok {
		key, own = p, false
		if action == "skipped" {
			action = "up-to-date" // a sub-thread's skip was printed above
		}
	}
	row, ok := r.rows[key]
	if !ok {
		row = &reportRow{rs: rs, action: action, own: own}
		r.rows[key] = row
		r.order = append(r.order, key)
		return
	}
	if own && !row.own {
		row.rs, row.own = rs, true // the conversation itself names the line
	}
	if actionRank[action] > actionRank[row.action] {
		row.action = action
	}
	if own && row.rs.Meta.Turns < rs.Meta.Turns {
		row.rs.Meta.Turns = rs.Meta.Turns
	}
}

// print lists the conversations written and returns how many.
func (r *restoreReport) print() int {
	n := map[string]int{}
	host, _ := os.Hostname()
	w := termWidth() - 40
	if w < 30 {
		w = 30
	}
	for _, k := range r.order {
		row := r.rows[k]
		n[row.action]++
		if row.action == "up-to-date" || row.action == "skipped" {
			continue
		}
		verb := map[string]string{"restored": "new", "appended": "updated", "remapped": "moved"}[row.action]
		from := ""
		if m := machineOf(row.rs); m != "-" && m != host && m != r.thisDevice {
			from = ", from " + shortHost(m)
		}
		turns := ""
		if row.rs.Meta.Turns > 0 {
			turns = fmt.Sprintf("%d turns", row.rs.Meta.Turns)
		} else {
			turns = humanize.IBytes(uint64(row.rs.Size))
		}
		fmt.Printf("  %-7s %s  %-6s %s (%s%s)\n", verb, short(row.rs.ID), row.rs.Meta.Agent, truncW(titleOf(row.rs), w), turns, from)
	}
	var parts []string
	for _, a := range []struct{ k, label string }{{"restored", "new"}, {"appended", "updated"}, {"remapped", "moved"}, {"up-to-date", "already up to date"}, {"skipped", "skipped"}} {
		if n[a.k] > 0 {
			parts = append(parts, fmt.Sprintf("%d %s", n[a.k], a.label))
		}
	}
	if len(parts) == 0 {
		parts = append(parts, "none")
	}
	line := "conversations: " + strings.Join(parts, ", ")
	if r.other > 0 {
		line += fmt.Sprintf("; %d memory/backup file(s) updated", r.other)
	}
	fmt.Println(line)
	return n["restored"] + n["appended"] + n["remapped"]
}

// restoreIncludeOnline: the default restore also brings conversations that
// machines still online are working on (restore --include-online).
var restoreIncludeOnline bool

// memoryInterval is how often the daemon brings the memory index up to
// date (other machines' new conversation cards and facts).
const memoryInterval = 5 * time.Minute

// restoreAskOnline: the caller (setup in a terminal) asks about the
// conversations left on online machines itself, so restoreAll does not
// print the hint.
var restoreAskOnline bool

func sumCounts(m map[string]int) (n int) {
	for _, v := range m {
		n += v
	}
	return n
}

func joinCounts(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for i, k := range keys {
		keys[i] = fmt.Sprintf("%s: %d", k, m[k])
	}
	return strings.Join(keys, ", ")
}

// logTeamTop runs SyncTeamTop from the daemon: renders and pushes when the
// automatic team links changed, and logs each change once.
func logTeamTop(e *client.Engine, seen map[string]string) {
	res, changed, err := e.SyncTeamTop(false)
	if err != nil {
		if msg := "team items: " + err.Error(); seen["use-top"] != msg {
			log.Print(msg)
			seen["use-top"] = msg
		}
		return
	}
	for _, r := range res {
		k := "use/" + r.Source + "/" + r.Type + "/" + r.Name
		if r.Op != "unchanged" && seen[k] != r.Op+r.Detail {
			log.Printf("team %s: %s %s %s v%d %s", r.Source, r.Op, r.Type, r.Name, r.Version, r.Detail)
		}
		seen[k] = r.Op + r.Detail
	}
	if !changed {
		return
	}
	if _, err := e.Render(false); err != nil {
		log.Printf("render: %v", err)
	}
	if _, err := e.LibPush(false); err != nil {
		log.Printf("library push: %v", err)
	}
}
