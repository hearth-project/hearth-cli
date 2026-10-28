# Security

## Reporting a vulnerability

Please report vulnerabilities privately through GitHub's "Report a vulnerability" (Security advisories) on this repository, not in a public issue. Include the version (`hearth --version`), what an attacker can do, and steps to reproduce. We aim to answer within a week.

## Model

- Sessions, memory notes, chosen files and credentials are encrypted on the client (Argon2id-derived keys, AEAD per chunk); the server stores ciphertext and has no key.
- The master password protects everything: any device that logs in with it can decrypt every credential (there is no approval by existing devices). Use a strong password; after a suspected leak run `hearth device revoke`, `hearth passwd` and rotate the credentials.
- The recovery key is the only way back after a forgotten password; `hearth recovery-key rotate` replaces it.
- In plaintext on the server: account emails, session sizes and timestamps, the user's library (skills, agents, recipes, settings; a secret scan blocks likely credentials before upload), team publications, and the site defaults (served without login unless `--site-auth`).
- A compromised client machine is out of scope: the cached vault key there can decrypt the account's data.
- Not implemented yet: MFA, email verification, an audit log of admin actions.
