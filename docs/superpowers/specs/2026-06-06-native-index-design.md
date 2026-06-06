# Native Package Index (Sub-project 1 of 5) — Design

Date: 2026-06-06
Status: approved (design), implementation in progress

## Goal of the larger effort

Stop invoking the `brew` binary. Keep Homebrew's *data* (formula/cask metadata,
bottles on GHCR). Reimplement every `brew`-CLI call against the filesystem +
Homebrew's JSON API + GHCR directly. One explicit, opt-in escape hatch remains:
source-only formulae (no bottle for the host's macOS tag) may fall back to
`brew install` behind `--allow-source`. bodega stays in Homebrew's prefix
(`/opt/homebrew`), fully interoperable.

Decomposed into 5 sub-projects: (1) native index, (2) native metadata reads,
(3) native mutations, (4) native casks, (5) native services/doctor/misc. This
spec covers **#1**.

## Sub-project 1 — Native Package Index

### Boundary

Own the metadata: fetch it, verify it, store it queryable, serve it fast — zero
`brew` invocation. Cuts the first brew binary call: `yum update` becomes "refresh
our index". Installer + `info` + `search` (already on the read-only API cache)
flip to the index here; remaining read shell-outs move in SP2.

### Why now

The existing `APICache` only *reads* `~/Library/Caches/Homebrew/api/*.jws.json`
and re-parses ~33 MB JSON per run. That cache does not even exist on a stock
`HOMEBREW_NO_INSTALL_FROM_API` machine — so bodega silently degrades to brew
subprocesses. Fetching + owning the index fixes correctness *and* speed.

### Package `internal/index`

Decoupled from the `brew` package name (it is the source of truth that outlives
"brew").

- `source.go` — `Source` interface; two impls, no plugin registry (YAGNI):
  - `NetworkSource`: HTTPS GET `https://formulae.brew.sh/api/{formula,cask}.jws.json`,
    `If-None-Match` via stored ETag. Endpoint confirmed to send `etag` +
    `cache-control: max-age=600`.
  - `BrewCacheSource`: read brew's local cache when present (offline bootstrap).
- `jws.go` — General-JWS verify, faithful port of Homebrew `api.rb`:
  - `alg: PS512` (RSASSA-PSS / SHA-512), `b64: false` + `crit: ["b64"]` (RFC 7797
    unencoded payload). Signing input = `protected_b64 + "." + raw_payload`.
  - `rsa.VerifyPSS(SHA512, salt=PSSSaltLengthEqualsHash, MGF1=SHA512)` against the
    pinned `homebrew-1` RSA public key (`keys/homebrew-1.pem`, `//go:embed`,
    identical to brew's bundled key). `kid` selects the key.
  - Verify failure → refuse to build (never install from an unverified index).
    `--insecure-index` config escape (HTTPS-trust only) for emergencies.
- `model.go` — `Formula`, `Cask`, `Bottle` (richer port of the `APIFormula`
  family).
- `schema.sql` (`//go:embed`) — SQLite (modernc, already a dep; FTS5 confirmed):
  ```
  formulae(name PK, full_name, tap, desc, license, homepage, stable_version, revision, raw)
  formula_deps(formula, dep, kind)            -- runtime|build
  formula_bottles(formula, tag, url, sha256, cellar)
  casks(token PK, name, desc, homepage, version, tap, raw)
  meta(key, value)                            -- formula_etag, cask_etag, built_at, schema_version
  formulae_fts(name, desc)                    -- FTS5; LIKE fallback
  ```
  `raw` per record = forward-compat for SP2–4. WAL mode → reads stay live during
  rebuild.
- `store.go` — `Open(path)`, `Lookup`, `LookupCask`, `Bottle(name,tag)`,
  `Search(q,limit)`, `Deps(name)`, `AllNames`, `BuiltAt`, `Close`.
- `build.go` — parse JWS payload once → one transactional rebuild (drop+insert in
  a tx; readers never see half).
- `refresh.go` — `EnsureFresh(ctx,maxAge)` (stale-gate on `meta.built_at`, ETag
  no-op) and `Refresh(ctx)` (forced; = `yum update`).

### Storage

`$XDG_DATA_HOME/yum/index.db` (default `~/.local/share/yum/index.db`), separate
from `history.db` — the index is disposable/rebuildable, the journal is precious.

### Refresh / freshness / migration

`EnsureFresh` source order: network → brew-cache bootstrap → existing stale
index. Fresh host fetches; a host that already has brew bootstraps offline; an
offline host with a stale index keeps working (fail-open reads, warn).
`yum update` forces `Refresh`. Replaces today's `brew.Stale(FETCH_HEAD)` gate.

### Error handling

- fetch fail + index present → warn, serve stale.
- fetch fail + no index + no brew cache → hard error: `run 'yum update' (needs network)`.
- JWS verify fail → refuse to build.
- interrupted rebuild → transactional; schema-version mismatch on open → rebuild.

### Speed / novelty

- warm `info`/`search`/`outdated`: one indexed query, sub-ms vs 200–600 ms reparse.
- `update`: ETag-gated, near-instant when unchanged.
- bodega works on a host where `brew` was never run.
- FTS5 ranked search; `Source` seam for future non-Homebrew sources.

### Testing (no network; `Source` injected)

JWS unwrap + verify (good/bad sig, legacy base64, malformed, wrong alg) · build
from fixture → assert rows · query API · ETag no-op · stale fail-open ·
brew-cache bootstrap · schema-version-bump rebuild. Fixtures signed with a test
key; verify uses an injectable pubkey in tests.

### Scope cuts (YAGNI)

No plugin registry · no cask-artifact normalization (raw now; SP4) · no
`provides` index (SP2) · no background daemon.

### Acceptance

1. `internal/index` fetches + verifies + builds + queries, fully tested.
2. `yum update` uses `index.Refresh` — `brew update` shell-out removed.
3. Installer + `yum info` + `yum search` read the index; `APICache` subsumed.
