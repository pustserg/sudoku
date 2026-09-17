CREATE TABLE users (
    id               BIGSERIAL PRIMARY KEY,
    provider         TEXT NOT NULL,
    provider_user_id TEXT NOT NULL,
    email            TEXT NOT NULL,
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, provider_user_id)
);

CREATE TABLE sessions (
    token_hash BYTEA PRIMARY KEY,
    user_id    BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE games (
    id           TEXT PRIMARY KEY,
    user_id      BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    difficulty   SMALLINT NOT NULL,
    givens       CHAR(81) NOT NULL,
    current      CHAR(81) NOT NULL,
    solution     CHAR(81) NOT NULL,
    mistakes     INT NOT NULL DEFAULT 0,
    max_mistakes INT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'in_progress',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX games_one_active_per_difficulty
    ON games (user_id, difficulty)
    WHERE status = 'in_progress';
