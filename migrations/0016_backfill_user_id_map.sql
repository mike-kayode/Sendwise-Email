-- Identity/legacy user pairs that projection could not reconcile: the
-- identity user's email already belonged to a different legacy users row and
-- re-keying that row onto the identity id was unsafe. internal/backfill and
-- the ensureActor self-healing path record the pair here and skip the row
-- instead of failing, so operators can resolve the duplicate by hand.
CREATE TABLE IF NOT EXISTS backfill_user_id_map (
    identity_user_id uuid PRIMARY KEY,
    legacy_user_id   uuid NOT NULL,
    email            text NOT NULL,
    reason           text NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now()
);
