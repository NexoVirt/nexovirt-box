# Security policy

NexoVirt Box runs as root helper plus web UI on your server, so we take reports seriously.

## Reporting a vulnerability

Please do **not** open a public issue for security problems.

Report it privately through GitHub: open the **Security** tab of this repository and choose
**Report a vulnerability** (GitHub private vulnerability reporting). Include:

- the Box version (`nexovirt-box version`) and the OS,
- what an attacker can do and what they need (network access, a Box account, a local user, ...),
- steps to reproduce or a proof of concept.

We confirm receipt, keep you updated while we work on a fix, and credit you in the release notes
unless you prefer otherwise. Please give us a reasonable time to ship a fix before you publish.

## Supported versions

Box is in its 0.x phase: only the latest release gets security fixes. Upgrade with
`curl -fsSL https://raw.githubusercontent.com/NexoVirt/nexovirt-box/main/deploy/get.sh | sudo sh`
or through Settings > Box updates.

## Scope

In scope: the Box binary (web UI and root helper), `deploy/install.sh`, `deploy/get.sh`, the release
and catalog signing, and the app catalog definitions in `catalog/`. The apps themselves (their Docker
images) belong to their own projects; report problems in them upstream.
