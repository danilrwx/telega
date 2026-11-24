# Contributing

Run these checks from the repository root with Go 1.27.3 and golangci-lint v2.15.0.

```sh
golangci-lint fmt
make lint
make check
make
./telega --demo
```

Tests use local fixtures and simulated Telegram responses.

- `cmd/telega` handles command-line arguments and version reporting.
- `internal/telega` handles terminal input, rendering, chat state, and the Telegram connection.
- `internal/session` loads credentials and reads and writes the private session file.

An annotated `v*` tag runs the release workflow. The tag annotation becomes the release notes. CI packages Linux and OpenBSD builds for amd64, arm64 and ARMv7. It also packages and launches the Intel and Apple Silicon builds on macOS runners. The workflow publishes all archives with `SHA256SUMS` after the checks pass. To test packaging locally, run:

```sh
./scripts/package.sh openbsd/armv7 v0.0.1 dist
```
