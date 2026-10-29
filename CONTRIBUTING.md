# Contributing

Thanks for helping. Hearth is a single Go module: client and server are one binary (`cmd/hearth`).

## Setup

```bash
git clone https://github.com/hearth-project/hearth-cli && cd hearth-cli
go build -o ~/.local/bin/hearth ./cmd/hearth      # Go 1.26+
```

## Before sending a change

```bash
gofmt -l .                       # must print nothing
go vet ./...
go test -race ./...
# optional: the same suite against PostgreSQL and S3
HEARTH_TEST_POSTGRES=postgres://user@127.0.0.1/postgres?sslmode=disable HEARTH_TEST_S3=fake go test -race ./...
hearth docs cli > docs/cli.md    # when commands or flags changed
```

`scripts/codex-e2e.sh` runs a real Codex against a real server (needs Codex installed and a working model provider); run it for changes to the Codex integration.

## Guidelines

- Keep agent-specific behaviour out of the core: new agents implement `adapter.Adapter` and `client.AgentIntegration` ([docs/en/agent-integration.md](docs/en/agent-integration.md)).
- The server must never see plaintext session content, titles or credentials. Anything new that leaves the client is either encrypted with the vault or documented as plaintext (like the library).
- Server performance matters: list and search paths read indexes only; no per-request logging.
- Docs come in English and Chinese (`docs/en`, `docs/zh`); update both, or say in the PR that a translation is missing.
- No company-specific hosts, proxies or credentials in code, tests or examples. Organisation defaults belong in a server's site directory ([server docs](docs/en/server.md#site-defaults-your-organisations-recipes-and-files)).

## License

By contributing you agree that your contributions are licensed under the [Apache License 2.0](LICENSE), as stated in its section 5.

## Reporting security issues

See [SECURITY.md](SECURITY.md); please do not open a public issue for vulnerabilities.
