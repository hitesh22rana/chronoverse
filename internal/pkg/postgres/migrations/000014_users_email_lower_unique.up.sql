-- Enforce case-insensitive email uniqueness alongside the existing UNIQUE on email.
CREATE UNIQUE INDEX IF NOT EXISTS idx_users_email_lower_unique
ON users (lower(email));
