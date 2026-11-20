# Contributing

Run these checks from the repository root with Go 1.27.3 and golangci-lint v2.15.0.

```sh
golangci-lint fmt
make lint
make check
make
./telega --demo
```

Tests use local fixtures and simulated Telegram responses. The terminal state, input and rendering are in `app.go`, `input.go` and `view.go`. `telegram.go` owns the connection and dispatches requests. Login and private session storage live in `login.go` and `session.go`.

An annotated `v*` tag runs the release workflow. The tag annotation becomes the release notes. CI builds archives for Linux and OpenBSD on amd64, arm64 and ARMv7, then publishes them with `SHA256SUMS` after the checks pass. To test the packaging command locally, run:

```sh
./scripts/package.sh openbsd/armv7 v0.0.1 dist
```
