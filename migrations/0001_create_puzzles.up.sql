CREATE TABLE puzzles (
    id         BIGSERIAL PRIMARY KEY,
    givens     CHAR(81) NOT NULL,
    solution   CHAR(81) NOT NULL,
    difficulty SMALLINT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (givens)
);
CREATE INDEX idx_puzzles_difficulty ON puzzles (difficulty);
