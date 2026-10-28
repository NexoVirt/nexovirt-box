# Keys

`catalog-dev.key` is the base64 Ed25519 seed that signs the built-in app catalog during development.
It is in the repository on purpose, so anyone can rebuild the catalog. It is NOT a release key: before a public
release, generate an offline key, keep it out of the repository, and put its public key into
`internal/catalog/key.go`.
