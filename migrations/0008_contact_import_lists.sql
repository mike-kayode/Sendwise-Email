ALTER TABLE contact_imports
    ADD COLUMN IF NOT EXISTS list_id uuid REFERENCES contact_lists(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS contact_imports_list_idx
    ON contact_imports (list_id)
    WHERE list_id IS NOT NULL;
