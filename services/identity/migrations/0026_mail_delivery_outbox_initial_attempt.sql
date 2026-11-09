ALTER TABLE mail_delivery_outbox
    DROP CONSTRAINT IF EXISTS mail_delivery_outbox_attempts_check;

ALTER TABLE mail_delivery_outbox
    ADD CONSTRAINT mail_delivery_outbox_attempts_check
    CHECK (attempts BETWEEN 0 AND 5);
