CREATE UNIQUE INDEX IF NOT EXISTS transactional_webhook_deliveries_event_uidx
    ON transactional_webhook_deliveries (webhook_id, message_event_id)
    WHERE message_event_id IS NOT NULL;
