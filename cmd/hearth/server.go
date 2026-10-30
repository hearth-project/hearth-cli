package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/dustin/go-humanize"
	"github.com/spf13/cobra"

	"github.com/hearth-project/hearth-cli/internal/logx"
	"github.com/hearth-project/hearth-cli/internal/server"
)

// storage holds the backend flags shared by server, invite, migrate and
// backup. Values default to $HEARTH_DB / $HEARTH_BLOBS so passwords
// and keys need not appear on the command line.
type storage struct{ data, db, blobs string }

func (st *storage) flags(c *cobra.Command) {
	c.Flags().StringVar(&st.data, "data", envOr("HEARTH_DATA", "./hearth-data"), "data directory (default database, blobs, TLS files) [$HEARTH_DATA]")
	c.Flags().StringVar(&st.db, "db", os.Getenv("HEARTH_DB"), "metadata database: empty = SQLite in --data, sqlite:///path, or postgres://user:pass@host/db [$HEARTH_DB]")
	c.Flags().StringVar(&st.blobs, "blobs", os.Getenv("HEARTH_BLOBS"), "blob store: empty = <data>/objects, file:///path, or s3://bucket/prefix?endpoint=host:9000 [$HEARTH_BLOBS]")
}

func (st *storage) persistentFlags(c *cobra.Command) {
	c.PersistentFlags().StringVar(&st.data, "data", envOr("HEARTH_DATA", "./hearth-data"), "server data directory [$HEARTH_DATA]")
	c.PersistentFlags().StringVar(&st.db, "db", os.Getenv("HEARTH_DB"), "metadata database URL [$HEARTH_DB]")
	c.PersistentFlags().StringVar(&st.blobs, "blobs", os.Getenv("HEARTH_BLOBS"), "blob store URL [$HEARTH_BLOBS]")
}

func (st *storage) config() server.Config {
	return server.Config{DataDir: st.data, Database: st.db, Blobs: st.blobs}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func serverCmd() *cobra.Command {
	var sto storage
	var addr, tlsCert, tlsKey, registration, publicURL, distDir, quota, siteDir string
	var identityBroker, identityBrokerKey string
	var siteAuth, noAutoUpdate, pinInstall, noVersionCheck bool
	var expireDays, graceDays, deviceDays int
	var gcInterval time.Duration
	c := &cobra.Command{
		Use:   "server",
		Short: "Run the sync server (SQLite + local directory by default; PostgreSQL and S3 optional)",
		RunE: func(*cobra.Command, []string) error {
			data := sto.data
			// A certificate made by `server gencert` is picked up automatically
			// and embedded into the installer so clients can trust it.
			var selfSigned []byte
			if tlsCert == "" {
				if pemData, err := os.ReadFile(filepath.Join(data, "tls.crt")); err == nil {
					tlsCert, tlsKey, selfSigned = filepath.Join(data, "tls.crt"), filepath.Join(data, "tls.key"), pemData
				}
			}
			var dist *server.Distribution
			if publicURL != "" {
				exe, err := os.Executable()
				if err != nil {
					return err
				}
				if distDir == "" {
					if fi, err := os.Stat(filepath.Join(data, "dist")); err == nil && fi.IsDir() {
						distDir = filepath.Join(data, "dist")
					}
				}
				dist = &server.Distribution{PublicURL: publicURL, BinaryPath: exe, Dir: distDir, CertPEM: selfSigned, PinInstall: pinInstall, NoAutoUpdate: noAutoUpdate, Version: version}
			}
			cfg := sto.config()
			cfg.Registration = registration
			cfg.ExpireAfter = time.Duration(expireDays) * 24 * time.Hour
			cfg.DeviceTTL = time.Duration(deviceDays) * 24 * time.Hour
			if deviceDays <= 0 {
				cfg.DeviceTTL = -1 // keep machines forever
			}
			cfg.Grace = time.Duration(graceDays) * 24 * time.Hour
			cfg.GCInterval = gcInterval
			cfg.Dist = dist
			cfg.SiteDir, cfg.SiteAuth = siteDir, siteAuth
			cfg.VersionCheck = !noVersionCheck
			cfg.IdentityBrokerURL, cfg.IdentityBrokerKey = identityBroker, identityBrokerKey
			q, err := parseQuota(quota)
			if err != nil {
				return err
			}
			cfg.DefaultQuota = max(q, 0)
			// Long-running: no per-request logs; repeated errors (a failing
			// disk, scanners on a public port) are collapsed to a few lines a
			// minute, TLS handshake noise is dropped.
			quiet := logx.NewLimiter(os.Stderr, 5, time.Minute, "TLS handshake error", "http: Accept error")
			log.SetOutput(quiet)
			cfg.Logger = log.New(quiet, "", log.LstdFlags)
			srv, err := server.New(cfg)
			if err != nil {
				return err
			}
			log.Printf("storage: %s; default quota per user: %s", srv.Storage(), quotaString(cfg.DefaultQuota))
			defer srv.Close()
			ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
			defer stop()
			go srv.GCLoop(ctx)

			hs := &http.Server{Addr: addr, Handler: srv, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 2 * time.Minute,
				ErrorLog: log.New(quiet, "", log.LstdFlags)}
			go func() {
				<-ctx.Done()
				sctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = hs.Shutdown(sctx)
			}()
			if cmdline := srv.InstallCommand(); cmdline != "" {
				log.Printf("client builds: %s", strings.Join(srv.DistPlatforms(), ", "))
				first := map[string]string{server.RegistrationOpen: "nothing to add, setup creates the account",
					server.RegistrationInvite: "append -s -- --invite <code> to sh (hearth server invite) to create the account",
					server.RegistrationClosed: "registration is closed: only existing accounts"}[cfg.Registration]
				log.Printf("new machines can install and restore with:\n  %s\n  (first machine of a new user: %s)", cmdline, first)
			}
			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return err
			}
			scheme := "http"
			if tlsCert != "" {
				scheme = "https"
				log.Printf("hearth server listening on https://%s (data %s, registration %s)", addr, data, registration)
			} else {
				log.Printf("hearth server listening on http://%s (data %s, registration %s); no TLS: put it behind an HTTPS proxy or use --tls-cert", addr, data, registration)
			}
			// For `hearth server start|status`: where this host reaches us.
			host, port, _ := net.SplitHostPort(ln.Addr().String())
			if ip := net.ParseIP(host); ip == nil || ip.IsUnspecified() {
				host = "127.0.0.1"
			}
			os.WriteFile(urlFile(dataDirOf([]string{"--data", data})), []byte(scheme+"://"+net.JoinHostPort(host, port)+"\n"), 0o600)
			if tlsCert != "" {
				err = hs.ServeTLS(ln, tlsCert, tlsKey)
			} else {
				err = hs.Serve(ln)
			}
			if errors.Is(err, http.ErrServerClosed) {
				return nil
			}
			return err
		},
	}
	c.Flags().StringVar(&addr, "addr", envOr("HEARTH_ADDR", "127.0.0.1:8787"), "listen address [$HEARTH_ADDR]")
	sto.flags(c)
	c.Flags().StringVar(&registration, "registration", envOr("HEARTH_REGISTRATION", server.RegistrationInvite), "who may register: invite, open, approval (admin approves each account) or closed; the admin panel can change it [$HEARTH_REGISTRATION]")
	c.Flags().StringVar(&tlsCert, "tls-cert", "", "TLS certificate file")
	c.Flags().StringVar(&tlsKey, "tls-key", "", "TLS key file")
	c.Flags().StringVar(&siteDir, "site", os.Getenv("HEARTH_SITE"), "organisation defaults for every user: recipes/, files/, learn.json (default <data>/site) [$HEARTH_SITE]")
	c.Flags().StringVar(&identityBroker, "identity-broker", os.Getenv("HEARTH_IDENTITY_BROKER_URL"), "platform identity broker for passwordless login, e.g. https://identity.example.com [$HEARTH_IDENTITY_BROKER_URL]")
	c.Flags().StringVar(&identityBrokerKey, "identity-broker-key", os.Getenv("HEARTH_IDENTITY_BROKER_KEY"), "system key used to sign identity-broker requests [$HEARTH_IDENTITY_BROKER_KEY]")
	c.Flags().BoolVar(&noVersionCheck, "no-version-check", os.Getenv("HEARTH_NO_VERSION_CHECK") == "1", "do not look up the latest Codex CLI / Claude Code / codex-mixin releases (npm registry, GitHub; every 6 hours, through $HTTPS_PROXY; $HEARTH_NPM_REGISTRY for a mirror) for the machines' environment check [$HEARTH_NO_VERSION_CHECK=1]")
	c.Flags().BoolVar(&siteAuth, "site-auth", os.Getenv("HEARTH_SITE_AUTH") == "1", "serve the site defaults only to logged-in users (default: anyone who can reach the server, so the installer gets them before login) [$HEARTH_SITE_AUTH=1]")
	c.Flags().BoolVar(&pinInstall, "pin-install", os.Getenv("HEARTH_PIN_INSTALL") == "1", "self-signed certificate: put --pinnedpubkey in the install one-liner, so even the first download is verified (default: plain -k; the installer then pins the certificate) [$HEARTH_PIN_INSTALL=1]")
	c.Flags().BoolVar(&noAutoUpdate, "no-client-auto-update", os.Getenv("HEARTH_NO_CLIENT_AUTO_UPDATE") == "1", "client daemons do not install new builds by themselves (hearth self-update still works) [$HEARTH_NO_CLIENT_AUTO_UPDATE=1]")
	c.Flags().StringVar(&distDir, "dist-dir", os.Getenv("HEARTH_DIST_DIR"), "directory with client builds hearth-<os>-<arch> for other platforms (scripts/build-dist.sh; default <data>/dist) [$HEARTH_DIST_DIR]")
	c.Flags().StringVar(&publicURL, "public-url", os.Getenv("HEARTH_PUBLIC_URL"), "URL clients use to reach this server; enables /install.sh [$HEARTH_PUBLIC_URL]")
	c.Flags().StringVar(&quota, "quota", envOr("HEARTH_QUOTA", "unlimited"), "default storage quota per user, e.g. 10GiB (per-user: hearth server admin quota) [$HEARTH_QUOTA]")
	c.Flags().IntVar(&expireDays, "expire-days", 30, "mark sessions expired after N days without access")
	c.Flags().IntVar(&deviceDays, "device-days", envInt("HEARTH_DEVICE_DAYS", 7), "forget machines not seen for N days (offline, reclaimed or revoked: their entry, health report and login; 0 = keep) [$HEARTH_DEVICE_DAYS]")
	c.Flags().IntVar(&graceDays, "grace-days", 7, "delete expired sessions after N more days")
	c.Flags().DurationVar(&gcInterval, "gc-interval", time.Hour, "how often to run expiry")
	c.AddCommand(inviteCmd(), gencertCmd(), migrateCmd(), backupCmd(), adminCmd(),
		serverStartCmd(), serverStopCmd(), serverRestartCmd(), serverStatusCmd(), superviseCmd(), serverInstallServiceCmd())
	return c
}

func gencertCmd() *cobra.Command {
	var data string
	var hosts []string
	var days int
	c := &cobra.Command{
		Use:   "gencert",
		Short: "Create a self-signed TLS certificate in the data directory",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := os.MkdirAll(data, 0o700); err != nil {
				return err
			}
			crt, key := filepath.Join(data, "tls.crt"), filepath.Join(data, "tls.key")
			if _, err := os.Stat(crt); err == nil {
				return fmt.Errorf("%s already exists; move it away to rotate (clients must reinstall ca.pem)", crt)
			}
			if err := server.GenerateSelfSigned(hosts, crt, key, time.Duration(days)*24*time.Hour); err != nil {
				return err
			}
			pemData, _ := os.ReadFile(crt)
			pin, _ := server.SPKIPin(pemData)
			fmt.Fprintf(cmd.OutOrStdout(), "wrote %s and %s\npin: %s\n", crt, key, pin)
			return nil
		},
	}
	c.Flags().StringVar(&data, "data", "./hearth-data", "server data directory")
	c.Flags().StringSliceVar(&hosts, "host", nil, "IP address or DNS name clients connect to (repeatable)")
	c.Flags().IntVar(&days, "days", 825, "validity in days")
	return c
}

// inviteCmd creates invite codes by opening the data directory directly, so
// only someone with access to the server host can issue them.
func inviteCmd() *cobra.Command {
	var sto storage
	var ttl time.Duration
	var count int
	c := &cobra.Command{
		Use:   "invite",
		Short: "Create single-use registration invite codes (run on the server host)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := sto.config()
			cfg.Logger = log.New(io.Discard, "", 0)
			srv, err := server.New(cfg)
			if err != nil {
				return err
			}
			defer srv.Close()
			for i := 0; i < count; i++ {
				code, err := srv.CreateInvite(ttl)
				if err != nil {
					return err
				}
				fmt.Fprintln(cmd.OutOrStdout(), code)
			}
			fmt.Fprintf(cmd.ErrOrStderr(), "%d invite(s), single use, valid for %s\n", count, ttl)
			return nil
		},
	}
	sto.flags(c)
	c.Flags().DurationVar(&ttl, "ttl", 7*24*time.Hour, "how long the invite stays valid")
	c.Flags().IntVar(&count, "count", 1, "number of invites")
	return c
}

func printMigrate(cmd *cobra.Command, st server.MigrateStats, err error) error {
	if err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "copied %d rows and %d blobs (%s)", st.Rows, st.Blobs, humanize.IBytes(uint64(st.BlobBytes)))
	if st.MissingBlobs > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "; %d blobs were deleted by GC during the copy", st.MissingBlobs)
	}
	fmt.Fprintln(cmd.OutOrStdout())
	return nil
}

// migrateCmd moves a deployment between backends, e.g. from the default
// single-machine SQLite + directory to PostgreSQL + S3.
func migrateCmd() *cobra.Command {
	var src storage
	var dst storage
	c := &cobra.Command{
		Use:   "migrate",
		Short: "Copy all data to other storage backends (e.g. SQLite + directory -> PostgreSQL + S3)",
		Long: "Copies every table and blob from the source (--data/--db/--blobs, default ./hearth-data) to an empty\n" +
			"destination (--to-data/--to-db/--to-blobs). The source is read in one snapshot; stop the server first\n" +
			"for a final cut-over so no writes are lost, then start it with the destination flags.",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if dst.db == "" && dst.blobs == "" && dst.data == "" {
				return fmt.Errorf("set --to-db and/or --to-blobs (or --to-data for SQLite + directory)")
			}
			if dst.data == "" {
				dst.data = src.data
			}
			if dst.data == src.data && dst.db == src.db {
				return fmt.Errorf("destination database is the source; set --to-db or --to-data")
			}
			logf := func(f string, a ...any) { fmt.Fprintf(cmd.ErrOrStderr(), f+"\n", a...) }
			st, err := server.Migrate(src.config(), dst.config(), logf)
			return printMigrate(cmd, st, err)
		},
	}
	src.flags(c)
	c.Flags().StringVar(&dst.data, "to-data", "", "destination data directory")
	c.Flags().StringVar(&dst.db, "to-db", os.Getenv("HEARTH_TO_DB"), "destination database URL [$HEARTH_TO_DB]")
	c.Flags().StringVar(&dst.blobs, "to-blobs", os.Getenv("HEARTH_TO_BLOBS"), "destination blob store URL [$HEARTH_TO_BLOBS]")
	return c
}

// backupCmd writes a consistent copy into a new directory (SQLite + files),
// whatever the live backends are. Restore by running the server on it or
// migrating it back.
func backupCmd() *cobra.Command {
	var src storage
	c := &cobra.Command{
		Use:   "backup <new-dir>",
		Short: "Write a consistent backup (SQLite + blob files) into a new directory while the server runs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, a []string) error {
			if entries, err := os.ReadDir(a[0]); err == nil && len(entries) > 0 {
				return fmt.Errorf("%s is not empty", a[0])
			}
			st, err := server.Migrate(src.config(), server.Config{DataDir: a[0]}, nil)
			if err == nil {
				fmt.Fprintf(cmd.OutOrStdout(), "backup in %s (serve it with: hearth server --data %s)\n", a[0], a[0])
			}
			return printMigrate(cmd, st, err)
		},
	}
	src.flags(c)
	return c
}

// envInt reads an integer environment variable, else def.
func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil {
		return v
	}
	return def
}
