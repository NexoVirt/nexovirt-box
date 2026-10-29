# Contributing to NexoVirt Box

Thanks for helping. Bug reports, app catalog fixes and pull requests are welcome.

## Before you start

- **Bugs and ideas**: open an issue with the templates. For bigger changes, open an issue first so we
  can agree on the approach before you spend time on it.
- **Security problems**: never in a public issue, see [SECURITY.md](SECURITY.md).
- **CLA**: every contribution needs a signed [Contributor License Agreement](CLA.md). The CLA bot
  comments on your first pull request; reply with the sentence it asks for. You sign once.

## Development

See [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md) for the design rules, the security model and how
the pieces fit together. The short version:

```sh
cd web && npm ci && npm run build && cd ..   # UI into web/dist (embedded into the binary)
go run . -dev -listen 127.0.0.1:8080 -state ./tmp
cd web && npm run dev                          # UI with hot reload, proxies /api
```

Checks that CI runs (please run them before you push):

```sh
go vet ./... && go test ./...
golangci-lint run
cd web && npm run check && npm run build
```

## Rules for pull requests

- Keep a pull request to one topic, with tests for new behaviour (Go tests next to the code).
- Root work goes into a new typed action in `helper.go`, never a command string from the web process.
- UI: plain CSS with the tokens in `web/src/app.css`, Lucide icons, the shared components in
  `web/src/lib/` (tables are always `DataTable.svelte`). No UI frameworks, no gradients.
- Text: short and plain; errors are friendly sentences, never raw HTTP errors. No em dashes.
- Commit messages: `type(scope): what changed` (feat, fix, refactor, docs, test, chore, ci).
- Catalog changes: edit `catalog/apps/<id>.json` and check with `go run ./cmd/catalog-check -dry-run`.
  Signing needs the offline catalog key, so maintainers re-sign after merging. To test a changed
  catalog locally, sign it with the dev key (`go run -tags devkey ./cmd/catalog-check -key keys/catalog-dev.key`) and run a build with
  `-tags devkey`; never commit a catalog signed with the dev key (CI refuses it). Never commit private keys.

## License

Box is AGPL-3.0. By contributing you agree that your work is published under that license and,
through the CLA, that NexoVirt may also license it under other terms.
