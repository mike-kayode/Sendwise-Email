-- Preserve the ownership TXT result separately from the overall domain state.
ALTER TABLE sending_domains
    ADD COLUMN IF NOT EXISTS verification_status text NOT NULL DEFAULT 'pending';

UPDATE sending_domains
SET verification_status = 'verified'
WHERE status = 'verified'
  AND verification_status = 'pending';
