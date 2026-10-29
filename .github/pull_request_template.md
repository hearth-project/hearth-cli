## Summary

<!-- What changes and why. Link the issue it fixes, e.g. "Fixes #123". -->

## Tests

<!-- What you ran and what you checked by hand. -->

## Checklist

- [ ] `gofmt -l .` prints nothing, `go vet ./...` and `go test -race ./...` pass
- [ ] `hearth docs cli > docs/cli.md` is regenerated (when commands or flags changed)
- [ ] Docs updated in both `docs/en` and `docs/zh`, or the missing translation is mentioned here
- [ ] `CHANGELOG.md` has a line under "Unreleased" (for user-visible changes)
- [ ] Nothing new leaves the client in plaintext, or it is documented as plaintext (see CONTRIBUTING)
