## What and why

<!-- What does this change, and which issue does it address? -->

## How it was tested

<!-- Tests added or run, manual checks on a test server. -->

## Checklist

- [ ] `go vet ./... && go test ./...` and `golangci-lint run -c .github/golangci.yml` pass
- [ ] `cd web && npm run check && npm run build` pass (UI changes)
- [ ] Root work is a typed helper action, no command strings from the web process
- [ ] Docs updated (README, docs/) where behaviour changed
- [ ] I signed the CLA (the bot asks on the first pull request)
