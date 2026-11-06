-- TASK D: server-side DKIM key storage for sending domains.
-- The backend generates a 2048-bit RSA DKIM keypair per sending domain and
-- stores the private key (AES-GCM encrypted with DKIM_ENCRYPTION_KEY) here.
-- The public key is exposed in DNS records and never persisted in plaintext.

ALTER TABLE sending_domains ADD COLUMN IF NOT EXISTS dkim_private_key text;
