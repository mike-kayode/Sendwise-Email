-- Egress probe hysteresis and gate control.
-- id is smallint; code must use integer 1 with typed params.
ALTER TABLE delivery_egress_health
    ADD COLUMN IF NOT EXISTS consecutive_failures integer NOT NULL DEFAULT 0;
