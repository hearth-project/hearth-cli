# Security

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub's "Report a vulnerability" (Security advisories) on this repository, not in a public issue. Include the version (`hearth --version`), what an attacker can do, and steps to reproduce. We aim to answer within a week.

## Model

- Sessions, memory notes, chosen files and credentials are encrypted on the client (Argon2id-derived keys, AEAD per chunk); the running service stores ciphertext.
- The master password protects everything: any device that logs in with it can decrypt every credential (there is no approval by existing devices). Use a strong password; after a suspected leak run `hearth device revoke`, `hearth passwd` and rotate the credentials.
- A Recovery Key recovers a forgotten password. `hearth recovery-key rotate` replaces it.
- An account may explicitly delegate recovery to an operator’s X25519 public key while it still has its master password. The operator with access to both the encrypted delegation and the separate private key can decrypt that account’s vault. Keep the private key offline, separate from server data and backups. Withdrawal removes the live delegation and any outstanding code. Anyone who previously copied a delegation and its private key retains that capability.
- An operator-issued code is a bearer credential for one email. Its holder can claim the wrapped vault and set a new password, including from a machine with no login. Codes expire after 30 minutes and issuing a replacement invalidates the previous code. The raw code and the new password stay out of server logs. A successful reset keeps current devices and the original Recovery Key valid.
- In plaintext on the server: account emails, session sizes and timestamps, the user's library (skills, agents, recipes, settings; a secret scan blocks likely credentials before upload), team publications, and the site defaults (served without login unless `--site-auth`).
- A compromised client machine is out of scope: the cached vault key there can decrypt the account's data.
- Not implemented yet: MFA, email verification ([roadmap](https://github.com/hearth-project/hearth-cli/issues/1)). Admin actions are written to an audit log (the macOS app, `GET /v1/admin/audit`).
