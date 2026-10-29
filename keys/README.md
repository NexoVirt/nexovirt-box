# Keys

`catalog-dev.key` is the base64 Ed25519 seed of the **development** catalog key. It is in the
repository on purpose, so anyone can sign a changed catalog for local testing. Only builds with
`-tags devkey` trust it (`internal/catalog/key_dev.go`); release builds do not, and a test
(`TestReleaseBuildDoesNotTrustDevKey`) keeps it that way.

The real keys are offline and never in this repository: the catalog key and the release key
(self-updates, `latest.json`). Their public halves are in `internal/catalog/key.go`; the release
key is also in `deploy/get.sh` and reaches the release workflow only as the `BOX_RELEASE_KEY`
repository secret.
