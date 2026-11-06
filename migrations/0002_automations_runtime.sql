-- Add the runtime dedupe index needed by the automation trigger engine.
CREATE UNIQUE INDEX IF NOT EXISTS automation_executions_unique_contact_run_idx
    ON automation_executions (automation_id, contact_id);

CREATE INDEX IF NOT EXISTS automation_steps_position_idx
    ON automation_steps (automation_id, position);
