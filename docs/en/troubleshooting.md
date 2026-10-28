# FAQ and troubleshooting

[中文](../zh/troubleshooting.md) · [Back to README](../../README.md)

Start with `hearth onboard status`: it checks install, login, workspace, background sync, agents and the skill, and lists the next steps in order.

## Installing

**`hearth: command not found` after installing.**
It is in `~/.local/bin`. In the current terminal run `export PATH="$HOME/.local/bin:$PATH"`. Current installers add it to `~/.bashrc` / `~/.profile`; older ones did not, so run the install command again.

**`certificate signed by unknown authority` / `x509` errors.**
The server is self-signed and this machine has no copy of its certificate. Re-run the admin's install command (`--install-only` is fine): it stores `~/.hearth/ca.pem`, which workspaces pick up automatically. Or `hearth setup --ca-cert <file>`. Do not replace the pin with `-k`.

**`curl: (90) SSL: public key does not match pinned public key`.**
The server certificate changed, or this is not your server. Ask the admin for the new command; do not drop the pin.

**The installer has no build for this platform.**
Builds exist for linux / darwin x amd64 / arm64; on Windows use WSL. Other platforms: the admin adds them with `scripts/build-dist.sh`, or build from source.

## Login and account

**Asked for an invite code.** The server is invite-only (`/v1/info` says `invite`); get a code from the admin and pass `--invite <code>`.

**`403 account disabled`.** An admin disabled the account.

**Forgot the master password.** `hearth recover` resets it with the recovery key; every device has to log in again. Without the recovery key the existing data cannot be decrypted.

**Someone saw the recovery key.** `hearth recovery-key rotate`; the old key stops working at once.

## Conversations

**Not visible in Codex Desktop after a restore.**
With Desktop running, `hearth session restore` makes the thread appear through its backend; if Desktop was not running, it lists the thread on its next start. Still missing: search for the title in Desktop, or run `hearth codex-threads`.

**`session is leased by another device`.** Another machine is writing that session. If it is gone, `hearth session take <id>`.

**`local file differs from server copy`.** Both sides changed. If the server copy is the one to keep: `hearth restore --force <id>`.

**Some sessions are not uploaded.**
- `hearth backup status`: maybe the category is turned off (e.g. `!@claude.sessions`);
- on shared accounts sessions that ran outside your workspace are not uploaded: `hearth session scope`, then `hearth session scope add <dir>` for directories that are yours;
- the background sync log (`hearth daemon status` prints its path) has a `skip` line with the reason.

**A deleted session came back / delete it for good.** Deleting it in Codex expires the server copy (removed after the grace period); `hearth session rm <id>` deletes it now.

## Library and credentials

**`library: refusing to upload: N likely secret(s) in the library`.**
The library (skills, agents, settings) is stored in plaintext, so an upload with likely secrets is refused, and library sync (including `config.toml` `[backup]`) stops until it is fixed. Move the real values into local auth files, back them up with `hearth secret set <name> --file <file> --restore-to <path>`, and have the skill read the file; mark lines that really are documented examples with a `hearth:allow-secret` comment.

**An auth file is missing on the new machine.** `hearth secret list` shows whether it was stored, `hearth secret restore` writes it back; if it never was, run `hearth learn` or `hearth secret set --file` on the original machine.

## Server

**`507` / `storage quota exceeded`.** The account is over its quota and uploads pause. The admin raises it (`hearth server admin quota <email> <size>`), or delete sessions you do not need.

**The server died and did not come back.** Run it with `hearth server start` (supervised) or systemd (`hearth server install-service`). After a container restart the entrypoint must run `hearth server start` again.

**Old client.** `hearth self-update`.

## Still stuck

Collect `hearth onboard status --json`, `hearth status` and the relevant lines of the background sync log (no secret values) and report them.
