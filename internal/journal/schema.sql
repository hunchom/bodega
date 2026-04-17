CREATE TABLE IF NOT EXISTS transactions (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    started_at   INTEGER NOT NULL,
    ended_at     INTEGER,
    verb         TEXT NOT NULL,
    exit_code    INTEGER,
    cmdline      TEXT NOT NULL,
    yum_version  TEXT NOT NULL,
    brew_version TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS transaction_packages (
    transaction_id INTEGER NOT NULL REFERENCES transactions(id) ON DELETE CASCADE,
    name           TEXT NOT NULL,
    from_version   TEXT,
    to_version     TEXT,
    source         TEXT NOT NULL,
    action         TEXT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_tx_started ON transactions(started_at);
CREATE INDEX IF NOT EXISTS idx_txp_name ON transaction_packages(name);
