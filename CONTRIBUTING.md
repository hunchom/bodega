# Contributing to bodega

## Local setup

```sh
git clone https://github.com/hunchom/bodega ~/bodega
cd ~/bodega
./scripts/build.sh
go test ./...
```

Requires Go 1.26 or newer. No CGO, no Xcode command-line tools needed.

## Workflow

1. Open an issue describing the change before large work — it's a small codebase and direction matters.
2. Branch off `main`.
3. Before committing:
   - `go vet ./...` clean
   - `gofmt -l .` empty
   - `go build ./...` succeeds
   - `go test ./...` passes
4. Commit messages follow Conventional Commits style (`feat:`, `fix:`, `docs:`, `chore:`, `test:`, `refactor:`, `build:`, `style:`).
5. One logical change per commit.

## Scope

Yes:

- New commands that extend the yum/dnf vocabulary sensibly
- Additional backends (mas, pipx, cargo, asdf) compiled into `internal/backend/`
- TUI polish, as long as it stays inside the existing visual language (single accent, no emojis, thin borders)
- Bug fixes

No:

- Emojis in output
- A plugin system (backends are compiled in on purpose)
- Web UI, daemon, remote sync
- Replacing brew as the underlying installer

## Code conventions

- One command per file under `internal/cmd/`.
- All rendering goes through `internal/ui/` — commands never call `fmt.Println` directly.
- Backends implement the `backend.Backend` interface exhaustively, even if some methods no-op.
- Tests use `testing.T.TempDir()` for any filesystem state; never touch the real `~/.local/share/yum/`.
- Keep comments minimal; named identifiers should carry the meaning.

## Releasing

Tag `vX.Y.Z` on `main`, GitHub Actions publishes darwin/arm64 and darwin/amd64 binaries as release artifacts. Update `CHANGELOG.md` in the same commit as the tag.
