-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TYPE user_status AS ENUM ('pending', 'active', 'disabled');
CREATE TYPE user_role AS ENUM ('administrator', 'operator', 'member');
CREATE TYPE resource_domain AS ENUM ('free', 'professional');
CREATE TYPE credential_status AS ENUM ('active', 'cooling', 'disabled');
CREATE TYPE request_status AS ENUM ('queued', 'dispatching', 'streaming', 'completed', 'failed', 'canceled', 'uncertain');
CREATE TYPE attempt_status AS ENUM ('created', 'sending', 'streaming', 'completed', 'failed', 'uncertain');
CREATE TYPE usage_source AS ENUM ('authoritative', 'estimated', 'unknown');
CREATE TYPE ledger_event_kind AS ENUM ('grant', 'reservation', 'settlement', 'release', 'compensation', 'adjustment', 'expiration');
CREATE TYPE plan_kind AS ENUM ('token', 'coding');
CREATE TYPE reservation_state AS ENUM ('reserved', 'settled', 'released', 'compensated');

CREATE TABLE system_state (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    bootstrapped_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO system_state (singleton) VALUES (true);

CREATE TABLE users (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    email text NOT NULL,
    display_name text NOT NULL,
    password_hash text NOT NULL,
    role user_role NOT NULL DEFAULT 'member',
    status user_status NOT NULL DEFAULT 'pending',
    approved_at timestamptz,
    disabled_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE invitations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    code_digest bytea NOT NULL UNIQUE,
    created_by uuid NOT NULL REFERENCES users(id),
    role user_role NOT NULL DEFAULT 'member',
    expires_at timestamptz NOT NULL,
    claimed_by uuid REFERENCES users(id),
    claimed_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((claimed_by IS NULL) = (claimed_at IS NULL))
);

CREATE TABLE sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_digest bytea NOT NULL UNIQUE,
    csrf_digest bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE gateway_keys (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name text NOT NULL,
    prefix text NOT NULL,
    secret_digest bytea NOT NULL UNIQUE,
    expires_at timestamptz,
    revoked_at timestamptz,
    last_used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE providers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug text NOT NULL UNIQUE,
    name text NOT NULL,
    kind text NOT NULL,
    base_url text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    source_url text,
    verified_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE models (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    public_name text NOT NULL UNIQUE,
    upstream_name text NOT NULL,
    display_name text NOT NULL,
    resource_domain resource_domain NOT NULL,
    capabilities jsonb NOT NULL DEFAULT '{}'::jsonb,
    enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider_id, upstream_name)
);

CREATE TABLE provider_credentials (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_id uuid NOT NULL REFERENCES providers(id) ON DELETE CASCADE,
    name text NOT NULL,
    encrypted_secret bytea NOT NULL,
    resource_domain resource_domain NOT NULL,
    status credential_status NOT NULL DEFAULT 'active',
    rpm_limit integer,
    tpm_limit bigint,
    concurrency_limit integer,
    fixed_proxy_url text,
    cooldown_until timestamptz,
    consecutive_failures integer NOT NULL DEFAULT 0,
    last_success_at timestamptz,
    last_error_kind text,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (rpm_limit IS NULL OR rpm_limit > 0),
    CHECK (tpm_limit IS NULL OR tpm_limit > 0),
    CHECK (concurrency_limit IS NULL OR concurrency_limit > 0)
);

CREATE TABLE credential_models (
    credential_id uuid NOT NULL REFERENCES provider_credentials(id) ON DELETE CASCADE,
    model_id uuid NOT NULL REFERENCES models(id) ON DELETE CASCADE,
    priority integer NOT NULL DEFAULT 100,
    weight integer NOT NULL DEFAULT 100 CHECK (weight > 0),
    PRIMARY KEY (credential_id, model_id)
);

CREATE TABLE model_authorizations (
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    model_id uuid NOT NULL REFERENCES models(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, model_id)
);

CREATE TABLE config_revisions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    revision bigint GENERATED ALWAYS AS IDENTITY UNIQUE,
    document jsonb NOT NULL,
    checksum text NOT NULL,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    published_by uuid REFERENCES users(id)
);

CREATE TABLE active_config (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    revision_id uuid NOT NULL REFERENCES config_revisions(id),
    version bigint NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE config_outbox (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    revision_id uuid NOT NULL REFERENCES config_revisions(id),
    active_version bigint NOT NULL,
    document jsonb NOT NULL,
    attempts integer NOT NULL DEFAULT 0,
    last_error text,
    delivered_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (revision_id, active_version)
);

CREATE TABLE entitlements (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    plan plan_kind NOT NULL,
    resource_domain resource_domain NOT NULL,
    model_id uuid REFERENCES models(id) ON DELETE CASCADE,
    granted_tokens bigint NOT NULL CHECK (granted_tokens > 0),
    starts_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    concurrency_limit integer NOT NULL CHECK (concurrency_limit > 0),
    rpm_limit integer CHECK (rpm_limit IS NULL OR rpm_limit > 0),
    tpm_limit bigint CHECK (tpm_limit IS NULL OR tpm_limit > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > starts_at)
);

CREATE TABLE requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    idempotency_key text CHECK (idempotency_key IS NULL OR length(idempotency_key) BETWEEN 1 AND 200),
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    user_id uuid NOT NULL REFERENCES users(id),
    gateway_key_id uuid NOT NULL REFERENCES gateway_keys(id),
    model_id uuid NOT NULL REFERENCES models(id),
    entitlement_id uuid NOT NULL REFERENCES entitlements(id),
    config_revision_id uuid REFERENCES config_revisions(id),
    resource_domain resource_domain NOT NULL,
    status request_status NOT NULL,
    stream boolean NOT NULL,
    input_tokens bigint,
    output_tokens bigint,
    usage_source usage_source NOT NULL DEFAULT 'unknown',
    error_kind text,
    error_detail text,
    accepted_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (gateway_key_id, idempotency_key)
);

CREATE TABLE request_attempts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id uuid NOT NULL REFERENCES requests(id) ON DELETE CASCADE,
    credential_id uuid NOT NULL REFERENCES provider_credentials(id),
    sequence integer NOT NULL,
    status attempt_status NOT NULL,
    upstream_request_id text,
    http_status integer,
    error_kind text,
    retry_after_at timestamptz,
    sent_at timestamptz,
    first_byte_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (request_id, sequence)
);

CREATE TABLE ledger_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id),
    entitlement_id uuid NOT NULL REFERENCES entitlements(id),
    request_id uuid REFERENCES requests(id),
    reservation_id uuid,
    kind ledger_event_kind NOT NULL,
    token_delta bigint NOT NULL,
    reserved_tokens bigint NOT NULL DEFAULT 0,
    input_tokens bigint NOT NULL DEFAULT 0,
    output_tokens bigint NOT NULL DEFAULT 0,
    usage_source usage_source NOT NULL DEFAULT 'unknown',
    source_event_id uuid,
    note text,
    created_by uuid REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE ledger_reservations (
    id uuid PRIMARY KEY,
    entitlement_id uuid NOT NULL REFERENCES entitlements(id),
    request_id uuid NOT NULL UNIQUE REFERENCES requests(id),
    state reservation_state NOT NULL,
    reserved_tokens bigint NOT NULL CHECK (reserved_tokens > 0),
    charged_tokens bigint NOT NULL DEFAULT 0 CHECK (charged_tokens >= 0),
    usage_source usage_source NOT NULL DEFAULT 'unknown',
    reserve_event_id uuid NOT NULL UNIQUE REFERENCES ledger_events(id),
    terminal_event_id uuid UNIQUE REFERENCES ledger_events(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id uuid REFERENCES users(id),
    action text NOT NULL,
    target_type text NOT NULL,
    target_id text,
    request_id uuid,
    detail jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE content_records (
    request_id uuid PRIMARY KEY REFERENCES requests(id) ON DELETE CASCADE,
    encrypted_content bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE response_records (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id uuid NOT NULL UNIQUE REFERENCES requests(id) ON DELETE CASCADE,
    status request_status NOT NULL,
    output jsonb,
    error jsonb,
    cancel_requested_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX sessions_active_digest_idx ON sessions (token_digest) WHERE revoked_at IS NULL;
CREATE UNIQUE INDEX users_email_lower_idx ON users (lower(email));
CREATE INDEX gateway_keys_active_digest_idx ON gateway_keys (secret_digest) WHERE revoked_at IS NULL;
CREATE INDEX provider_credentials_eligible_idx ON provider_credentials (provider_id, resource_domain, status, cooldown_until);
CREATE INDEX entitlements_applicable_idx ON entitlements (user_id, resource_domain, expires_at, starts_at);
CREATE INDEX requests_user_created_idx ON requests (user_id, accepted_at DESC);
CREATE INDEX requests_status_idx ON requests (status, updated_at);
CREATE INDEX request_attempts_request_idx ON request_attempts (request_id, sequence);
CREATE INDEX ledger_events_user_created_idx ON ledger_events (user_id, created_at DESC);
CREATE INDEX ledger_events_entitlement_created_idx ON ledger_events (entitlement_id, created_at, id);
CREATE UNIQUE INDEX ledger_events_source_event_idx ON ledger_events (source_event_id) WHERE source_event_id IS NOT NULL;
CREATE INDEX audit_events_created_idx ON audit_events (created_at DESC);
CREATE INDEX content_records_expiry_idx ON content_records (expires_at);
CREATE INDEX config_outbox_pending_idx ON config_outbox (id) WHERE delivered_at IS NULL;

-- +goose Down
DROP TABLE IF EXISTS response_records;
DROP TABLE IF EXISTS content_records;
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS ledger_reservations;
DROP TABLE IF EXISTS ledger_events;
DROP TABLE IF EXISTS request_attempts;
DROP TABLE IF EXISTS requests;
DROP TABLE IF EXISTS entitlements;
DROP TABLE IF EXISTS config_outbox;
DROP TABLE IF EXISTS active_config;
DROP TABLE IF EXISTS config_revisions;
DROP TABLE IF EXISTS model_authorizations;
DROP TABLE IF EXISTS credential_models;
DROP TABLE IF EXISTS provider_credentials;
DROP TABLE IF EXISTS models;
DROP TABLE IF EXISTS providers;
DROP TABLE IF EXISTS gateway_keys;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS invitations;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS system_state;
DROP TYPE IF EXISTS plan_kind;
DROP TYPE IF EXISTS reservation_state;
DROP TYPE IF EXISTS ledger_event_kind;
DROP TYPE IF EXISTS usage_source;
DROP TYPE IF EXISTS attempt_status;
DROP TYPE IF EXISTS request_status;
DROP TYPE IF EXISTS credential_status;
DROP TYPE IF EXISTS resource_domain;
DROP TYPE IF EXISTS user_role;
DROP TYPE IF EXISTS user_status;
