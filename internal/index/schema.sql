-- bodega native package index. Rebuilt wholesale on `yum update`; disposable
-- (the journal at history.db is the precious one). schema_version in meta gates
-- a rebuild when this file changes.

CREATE TABLE IF NOT EXISTS formulae (
  name           TEXT PRIMARY KEY,
  full_name      TEXT NOT NULL DEFAULT '',
  tap            TEXT NOT NULL DEFAULT '',
  desc           TEXT NOT NULL DEFAULT '',
  license        TEXT NOT NULL DEFAULT '',
  homepage       TEXT NOT NULL DEFAULT '',
  stable_version TEXT NOT NULL DEFAULT '',
  revision       INTEGER NOT NULL DEFAULT 0,
  raw            TEXT NOT NULL DEFAULT ''
);

-- normalized deps power SP2/SP3 (deps tree, reverse-deps, autoremove) as SQL.
CREATE TABLE IF NOT EXISTS formula_deps (
  formula TEXT NOT NULL,
  dep     TEXT NOT NULL,
  kind    TEXT NOT NULL  -- 'runtime' | 'build'
);
CREATE INDEX IF NOT EXISTS idx_formula_deps_formula ON formula_deps(formula);
CREATE INDEX IF NOT EXISTS idx_formula_deps_dep     ON formula_deps(dep);

-- per-tag bottle entries; the installer resolves a URL+sha here without
-- re-parsing the raw payload.
CREATE TABLE IF NOT EXISTS formula_bottles (
  formula TEXT NOT NULL,
  tag     TEXT NOT NULL,
  url     TEXT NOT NULL,
  sha256  TEXT NOT NULL,
  cellar  TEXT NOT NULL DEFAULT '',
  PRIMARY KEY (formula, tag)
);

CREATE TABLE IF NOT EXISTS casks (
  token    TEXT PRIMARY KEY,
  name     TEXT NOT NULL DEFAULT '',  -- newline-joined name[]
  desc     TEXT NOT NULL DEFAULT '',
  homepage TEXT NOT NULL DEFAULT '',
  version  TEXT NOT NULL DEFAULT '',
  tap      TEXT NOT NULL DEFAULT '',
  raw      TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

-- FTS5 ranked search over names + descriptions (formulae and casks).
CREATE VIRTUAL TABLE IF NOT EXISTS search_fts USING fts5(
  name, desc, source UNINDEXED, tokenize = 'unicode61'
);
