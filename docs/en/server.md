# Running and administering the server

[中文](../zh/server.md) · [Back to README](../../README.md)

Server and client are the same `hearth` binary. The server stores ciphertext (sessions, credentials) and each user's own library; it cannot decrypt anything.

## Storage

| | Default | Optional (several instances, large data) |
| --- | --- | --- |
| metadata (`--db`, `$HEARTH_DB`) | SQLite in the `--data` directory | `postgres://user:pass@host/db` |
| blobs (`--blobs`, `$HEARTH_BLOBS`) | `<data>/objects` | `s3://bucket/prefix?endpoint=host:9000&path_style=1` (AWS S3, MinIO, BOS, OSS, COS, Ceph); keys from `$HEARTH_S3_ACCESS_KEY` / `$HEARTH_S3_SECRET_KEY` or `AWS_*` |

SQLite + a directory loses no features; its limits are operational: one instance, one machine's disk, and a persistent volume in containers. For several instances behind a load balancer use PostgreSQL + S3; `hearth server migrate` moves the data at any time.

## One machine (the usual start)

```bash
D=/var/lib/hearth
go build -o /usr/local/bin/hearth ./cmd/hearth            # or download the binary from an existing server

# self-signed TLS; --host is the IP / name clients use (repeatable). Skip behind a TLS-terminating proxy.
hearth server gencert --data $D --host hearth.example.com

# client builds to serve (linux/darwin x amd64/arm64); without this only the server's own platform is served
scripts/build-dist.sh $D/dist

# in the background under a supervisor (restarts on exit or 3 failed /healthz checks; rotated log)
hearth server start --data $D --addr 0.0.0.0:8787 \
  --public-url https://hearth.example.com:8787 --registration open
hearth server status --data $D
```

Startup prints the install command with the key pin. Share the home page `https://hearth.example.com:8787/`: it has the install command, download links and SHA-256 checksums in Chinese and English.

- `--registration`: `invite` (default, a code per new user), `open` (internal networks: anyone who can reach it), `closed`.
- `--public-url`: the URL clients use; enables `/install.sh`, `/download/*` and the download page.
- Every flag has an environment variable (`HEARTH_DATA`, `HEARTH_ADDR`, `HEARTH_REGISTRATION`, `HEARTH_PUBLIC_URL`, `HEARTH_QUOTA`, …), so passwords stay off the command line.
- Plain HTTP is refused except on loopback: use `server gencert`, `--tls-cert/--tls-key` or a reverse proxy.

### Starting at boot

- **systemd**: `hearth server install-service <same flags>` prints a unit (`Restart=always`); save it as `/etc/systemd/system/hearth.service` and `systemctl enable --now hearth`. `deploy/hearth.service` is an example.
- **Containers / no systemd**: put `hearth server start …` in the entrypoint or an `@reboot` cron job; running it twice does not start a second server.
- `hearth server stop | restart | status --data <dir>`.

### Logs

No per-request logging. Startup prints a few lines, GC one line when it changed something; repeated errors are collapsed to 5 a minute plus a count, TLS handshake noise is dropped. Under the supervisor the log is `<data>/server.log`, rotated at 10 MiB with 5 files kept (60 MiB at most).

## Docker

```bash
docker compose -f deploy/docker-compose.yml up -d                            # one container, SQLite + a volume
docker compose -f deploy/docker-compose.yml exec hearth hearth server invite  # invite codes
docker compose -f deploy/docker-compose.scale.yml up -d --scale hearth=2      # PostgreSQL + MinIO + replicas
```

The installer is served once `HEARTH_PUBLIC_URL` is set and TLS is provided (`hearth server gencert` into the volume, or an HTTPS reverse proxy). The Docker files are not tested in CI yet.

## Site defaults (your organisation's recipes and files)

Company-specific setup (internal git hosts, proxies, model-gateway install recipes, where the company SSO token lives) does not belong in the open-source code; put it in the server's site directory. Default `<data>/site` (`--site` or `$HEARTH_SITE`), laid out like a personal library:

```text
<data>/site/
  recipes/*.toml     restore recipes for every user (install tools, write configs, checks, manual steps)
  files/...          files those recipes install (e.g. an ssh config template)
  files/client-env.md   your notes for the client software guide (mirrors, proxies, gateway presets); `hearth docs env` prints them after the guide
  learn.json         {"token_files": ["~/.config/sso/token_*"], "identity": {"email_domain": "example.com"}}: login-token globs and local-identity settings
```

- **The installer fetches it before login** (`hearth site pull --server <url>`); `hearth setup`, `hearth recipe apply` and the background library sync (20 min) refresh it. `GET /v1/site` needs no login by default (304 when unchanged); the copy lives in `<hearth home>/site`. `--site-auth` restricts it to logged-in users (then it applies only after login). Edits apply within 10 s without restarting the server.
- A recipe or file of the same name in the user's own library wins; `hearth site disable <recipe>` opts a user out (synced with the account). `hearth site status` lists what this machine has.
- Steps that run commands need the user's approval on each machine (`--yes`), as with personal recipes.
- Site recipes are for everyone: do not assume a user's secret names; guard such steps with `when_secret = "<name>"`.
- Site content is served in plaintext to everyone who can reach the server (every logged-in user with `--site-auth`): **no credentials** there; users keep theirs with `hearth secret`.
- `learn.json` `token_files` are also where `hearth login` / `hearth whoami` look for a local identity. `identity.email_domain` turns a user name found in one of those tokens into the account email, and `identity.username_claim` names the JWT claim to read (default `username`). Without a domain, the mail domain of the user's git email is used.

### Passwordless login through a platform broker

On a platform that injects a per-user credential into each job container, the server can delegate identity checks to its broker:

```bash
hearth server start ... \
  --identity-broker https://identity.example.com \
  --identity-broker-key <system key>          # or $HEARTH_IDENTITY_BROKER_URL / _KEY
```

- The server calls `GET <broker>/v3/user/userAuth?userName=<erp>` with `Authorization: Bearer <unix-ts>/<hex HMAC-SHA256(key, ts)>` and reads `data.sk`. The system key never reaches clients.
- No user command: while logged in, `hearth login`, `hearth setup` and the daemon start check the account once and upload the vault key wrapped by a key derived from the platform secret when the container has it. `hearth login` on a new machine signs a timestamped proof with the same secret; the server resolves the secret through the broker and verifies it. No password is asked. `hearth identity enable` only forces the upload again (for example to debug).
- `hearth identity disable` removes the passwordless path and is sticky: the automatic check will not re-enable it. Login still works with the master password.
- **Security:** whoever holds the platform secret can unlock that account's vault, and the broker key can fetch any user's secret — protect it like a root credential. Accounts created before a broker is configured are unaffected until they run `identity enable`.

## Users and quotas

Admin commands open the server's database and blob store directly (same flags as `hearth server`), work while the server runs and need no admin password; changes reach every instance within 30 s.

```bash
hearth server invite --data $D                         # single-use invite codes (valid 7 days by default)
hearth server admin invites | invites revoke <code>
hearth server admin users --data $D                    # accounts and usage, largest first
hearth server admin user <email>
hearth server admin quota <email> 20GiB | unlimited | default
hearth server admin disable <email> | enable <email>   # block login, keep data
hearth server admin revoke-devices <email>             # log out every device (leaked password)
hearth server admin delete-user <email>                # delete the account and all its data
hearth server admin stats                              # totals
hearth server admin gc                                 # run expiry now and delete orphaned blobs
```

The default quota comes from `hearth server --quota 10GiB` (unlimited by default). A client over its quota pauses uploads and says so in `hearth status`.

## Web console and admin panel

The server ships a web console at `/app` (`/admin` is the same page). Every registered user signs in with email and master password and sees their machines (online, name, IP, client version; log one out), the skills and agents of their library (most used first, resident / on demand, shared with one click to teams where they are maintainer or owner, after a secret scan), their most used skills and agents, and their teams with members and shared items (maintainers make invite codes); conversations and credentials are end-to-end encrypted and not readable there. Admins also get the administration pages: a dashboard (accounts, active users, uploads per day, sign-ups, top skills, client versions in use), users (each one's clients online now: machine, IP, version; disable / enable, quota, admin, log out devices, delete; click an email for all devices), sign-up approvals, invite codes, teams (create with an owner, add / remove members, change roles, shared items, team-to-team sharing, delete), runtime settings (registration mode, default quota, client auto-update; no restart, every instance within 10 s), maintenance (expiry, orphaned blobs) and an audit log. English and Chinese.

```bash
hearth server admin grant <email> --data $D    # on the server: make an account an admin (ungrant, admins)
# open https://<server>/admin and sign in with the account (email + master password)
hearth panel                                  # or, on a machine logged in as an admin: a 10-minute sign-in link, opened in the browser
hearth server admin link <email> --url <public-url> --data $D   # or a link straight from the server
```

With the account sign-in the browser derives the login key from the master password the way the client does (Argon2id + HKDF) and sends only that key; the password never leaves the browser (a few seconds with the default parameters). Signing in (or opening a link) gives a signed 12-hour session cookie (HttpOnly, SameSite=Strict); changes also need the `X-Hearth-Admin` header (CSRF), and the page has a strict CSP. Removing an admin or disabling the account ends the session at once. Every change is written to the audit log (command-line changes as `cli`).

**Sign-up approval**: with registration `approval` (panel settings, or `hearth server --registration approval`) anyone may sign up, but the account cannot log in until an admin approves it on the Approvals page (or `hearth server admin approve <email>`); the client says it is waiting.

## Session expiry

- Sessions not accessed for `--expire-days` (default 30) are marked expired and deleted `--grace-days` (default 7) later; pinned sessions and long-term memory never expire.
- Sessions deleted by the user in Codex are marked expired by the client and deleted after the same grace period.
- Restoring by id during the grace period revives a session.

## Backups and migration

```bash
hearth server backup /backups/hearth-$(date +%F) --data $D                   # consistent online backup (SQLite + blob files)
hearth server stop --data $D && hearth server migrate --data $D --to-db postgres://… --to-blobs s3://…   # migrate (stop first; empty destination)
```

## Releasing a new client version

```bash
go build -o /usr/local/bin/hearth ./cmd/hearth
scripts/build-dist.sh $D/dist <version>
hearth server restart --data $D
```

Online clients' daemons then update themselves within 30 minutes (refreshing the `hearth` skill in their agents too); `hearth self-update` updates right away. Start the server with `--no-client-auto-update` to stop clients from updating by themselves.

## Performance

Search and restore speed come first: sessions are indexed by last activity and paged, listing reads only the index, blobs are stored as per-session append chunks with sequential reads and writes. With several instances the per-instance caches are bounded: a revoked token stops working everywhere within 30 s, team publications are searchable within 2 s, GC runs on one instance at a time. Benchmarks: [technical reference](reference.md#server-performance).

## Security

- Invite-only by default; only the SHA-256 of an invite code is stored.
- Every session and device endpoint needs a token; auth endpoints are rate limited per IP; tokens expire after 30 days without use.
- A new device that logs in with the master password can decrypt every credential (no approval by an existing device). The master password is the only protection of the credentials; MFA and email verification are not implemented yet.
- Admins see account emails, usage and metadata sizes, not session content, titles or credentials. The library is stored in plaintext (visible to its owner; team publications to the team); clients scan it and block likely secrets before upload.
