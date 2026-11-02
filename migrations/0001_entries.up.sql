BEGIN;

-- Local users are independent of future authentication-provider identities.
CREATE TABLE users (
    id text PRIMARY KEY CHECK (length(btrim(id)) > 0),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE entries (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner_id text NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    title text NOT NULL CHECK (length(btrim(title)) BETWEEN 1 AND 200),
    notes text NOT NULL DEFAULT '' CHECK (length(notes) <= 20000),
    occurred_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX entries_owner_id_id_idx ON entries (owner_id, id DESC);

COMMIT;
