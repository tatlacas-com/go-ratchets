# CLAUDE.md

`go-ratchets` holds shrink-only code-health ratchets for Go repos, shared by fompiler-cli and Brevwick so both enforce one implementation: `cmd/sizelint` (file, function and script size limits) and `cmd/archlint` (import-direction rules). Each has a baseline of known violations that may only shrink.

- Stdlib only. Every change via PR; small, one concern each, opened ready. Branch `<feat|fix|chore|docs|test>/issue-<N>-<slug>`, conventional commits ≤72 chars, body `Closes #N`.
- Stage explicit paths. No AI attribution lines. Never `--no-verify`, `--amend`, `--force`, `--admin` or `--bypass`.
- Before a PR: `gofmt -l .`, `go vet ./...`, `go test ./...`, and the tools on this repo (`go run ./cmd/sizelint`, `go run ./cmd/archlint`). CI is the gate.
- Tests are hermetic (temp dirs, no network). Fix every real review finding in the same PR; no follow-up issues.
- Files ≤800 lines, functions ≤80.
