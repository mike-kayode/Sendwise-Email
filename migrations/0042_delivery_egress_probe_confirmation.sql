ALTER TABLE delivery_egress_health
  ADD COLUMN IF NOT EXISTS unconfirmed_failure boolean NOT NULL DEFAULT false;
