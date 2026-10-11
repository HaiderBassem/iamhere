-- Built-in accounts (email + password), replacing the external identity
-- provider. A user signed up here has idp_issuer = 'local'.

-- Sign-in details. The email is stored lower-case and is the login name.
CREATE TABLE accounts (
    user_id             uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    email               text NOT NULL UNIQUE CHECK (email = lower(email) AND length(email) BETWEEN 3 AND 254),
    password_hash       text NOT NULL,
    email_verified_at   timestamptz,
    password_changed_at timestamptz NOT NULL DEFAULT now(),
    created_at          timestamptz NOT NULL DEFAULT now()
);

-- Signed-in devices. Only the SHA-256 of the bearer token is stored, so a
-- database leak does not leak usable sessions.
CREATE TABLE auth_sessions (
    id           uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash   bytea NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    user_agent   text CHECK (user_agent IS NULL OR length(user_agent) <= 300),
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL
);
CREATE INDEX auth_sessions_user_idx ON auth_sessions (user_id);
CREATE INDEX auth_sessions_expiry_idx ON auth_sessions (expires_at);

-- One-time links sent by email (confirm the address, reset the password).
-- Hashed like sessions; single use; short-lived.
CREATE TABLE auth_tokens (
    token_hash bytea PRIMARY KEY CHECK (length(token_hash) = 32),
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose    text NOT NULL CHECK (purpose IN ('verify_email', 'reset_password')),
    email      text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    expires_at timestamptz NOT NULL,
    used_at    timestamptz
);
CREATE INDEX auth_tokens_user_idx ON auth_tokens (user_id, purpose);
CREATE INDEX auth_tokens_expiry_idx ON auth_tokens (expires_at);
