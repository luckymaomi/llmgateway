-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TYPE user_status AS ENUM ('active', 'disabled', 'deleted');
CREATE TYPE user_role AS ENUM ('administrator', 'member');
CREATE TYPE resource_pool_status AS ENUM ('active', 'disabled', 'retired');
CREATE TYPE credential_status AS ENUM ('active', 'cooling', 'disabled', 'retired');
CREATE TYPE service_plan_status AS ENUM ('active', 'disabled', 'archived');
CREATE TYPE subscription_status AS ENUM ('scheduled', 'active', 'suspended', 'canceled', 'expired');
CREATE TYPE request_status AS ENUM ('queued', 'dispatching', 'streaming', 'completed', 'failed', 'canceled', 'uncertain');
CREATE TYPE response_status AS ENUM ('queued', 'in_progress', 'completed', 'failed', 'canceled', 'uncertain');
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
    display_name text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 120),
    password_hash text NOT NULL,
    role user_role NOT NULL DEFAULT 'member',
    status user_status NOT NULL DEFAULT 'active',
    disabled_at timestamptz,
    deleted_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'disabled') = (disabled_at IS NOT NULL)),
    CHECK ((status = 'deleted') = (deleted_at IS NOT NULL)),
    CHECK (status <> 'deleted' OR disabled_at IS NULL)
);

CREATE TABLE site_profile (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    name text NOT NULL CHECK (char_length(name) BETWEEN 2 AND 80),
    description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 240),
    contact text NOT NULL DEFAULT '' CHECK (char_length(contact) <= 200),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    updated_by uuid REFERENCES users(id),
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO site_profile (singleton, name) VALUES (true, 'LLMGateway');

CREATE TABLE member_mutations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id uuid NOT NULL REFERENCES users(id),
    action text NOT NULL,
    idempotency_key uuid NOT NULL,
    user_id uuid REFERENCES users(id),
    request_fingerprint bytea NOT NULL CHECK (octet_length(request_fingerprint) = 32),
    request_id text NOT NULL,
    encrypted_one_time_secret bytea,
    result jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (actor_user_id, action, idempotency_key)
);

CREATE TABLE sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id),
    token_digest bytea NOT NULL UNIQUE,
    csrf_digest bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    revoked_at timestamptz,
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE gateway_keys (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    prefix text NOT NULL,
    secret_digest bytea NOT NULL UNIQUE,
    expires_at timestamptz,
    revoked_at timestamptz,
    last_used_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE gateway_key_mutations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id uuid NOT NULL REFERENCES users(id),
    idempotency_key uuid NOT NULL,
    request_fingerprint bytea NOT NULL CHECK (octet_length(request_fingerprint) = 32),
    request_id text NOT NULL,
    gateway_key_id uuid REFERENCES gateway_keys(id),
    result jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (actor_user_id, idempotency_key)
);

-- Providers and models are catalog projections created only from validated code definitions.
CREATE TABLE providers (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    catalog_id text NOT NULL UNIQUE,
    slug text NOT NULL UNIQUE,
    name text NOT NULL,
    kind text NOT NULL,
    base_url text NOT NULL,
    source_url text NOT NULL,
    verified_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE models (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_id uuid NOT NULL REFERENCES providers(id),
    public_name text NOT NULL UNIQUE,
    upstream_name text NOT NULL,
    display_name text NOT NULL,
    capabilities jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (provider_id, upstream_name)
);

CREATE TABLE resource_pools (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    provider_id uuid NOT NULL REFERENCES providers(id),
    slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$'),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    status resource_pool_status NOT NULL DEFAULT 'active',
    retired_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'retired') = (retired_at IS NOT NULL))
);

CREATE TABLE resource_pool_models (
    resource_pool_id uuid NOT NULL REFERENCES resource_pools(id),
    model_id uuid NOT NULL REFERENCES models(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (resource_pool_id, model_id)
);

CREATE TABLE resource_pool_mutations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id uuid NOT NULL REFERENCES users(id),
    action text NOT NULL,
    idempotency_key uuid NOT NULL,
    request_fingerprint bytea NOT NULL CHECK (octet_length(request_fingerprint) = 32),
    request_id text NOT NULL,
    resource_pool_id uuid REFERENCES resource_pools(id),
    result jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (actor_user_id, action, idempotency_key)
);

CREATE TABLE model_price_versions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    model_id uuid NOT NULL REFERENCES models(id),
    currency text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    input_rate_nanos_per_million bigint NOT NULL CHECK (input_rate_nanos_per_million BETWEEN 0 AND 1000000000000000),
    output_rate_nanos_per_million bigint NOT NULL CHECK (output_rate_nanos_per_million BETWEEN 0 AND 1000000000000000),
    effective_at timestamptz NOT NULL,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (model_id, effective_at)
);

CREATE TABLE model_price_mutations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id uuid NOT NULL REFERENCES users(id),
    idempotency_key uuid NOT NULL,
    request_fingerprint bytea NOT NULL CHECK (octet_length(request_fingerprint) = 32),
    request_id text NOT NULL,
    price_version_id uuid REFERENCES model_price_versions(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (actor_user_id, idempotency_key)
);

-- +goose StatementBegin
CREATE FUNCTION reject_immutable_record_mutation() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
    RAISE EXCEPTION '% records are immutable', TG_TABLE_NAME USING ERRCODE = '55000';
END;
$$;
-- +goose StatementEnd

CREATE TRIGGER model_price_versions_immutable
BEFORE UPDATE OR DELETE ON model_price_versions
FOR EACH ROW EXECUTE FUNCTION reject_immutable_record_mutation();

CREATE TABLE provider_credentials (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    resource_pool_id uuid NOT NULL REFERENCES resource_pools(id),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    encrypted_secret bytea NOT NULL,
    status credential_status NOT NULL DEFAULT 'active',
    rpm_limit integer CHECK (rpm_limit IS NULL OR rpm_limit > 0),
    tpm_limit bigint CHECK (tpm_limit IS NULL OR tpm_limit > 0),
    concurrency_limit integer CHECK (concurrency_limit IS NULL OR concurrency_limit > 0),
    cooldown_until timestamptz,
    consecutive_failures integer NOT NULL DEFAULT 0,
    last_success_at timestamptz,
    last_error_kind text,
    last_probe_at timestamptz,
    last_probe_latency_ms bigint CHECK (last_probe_latency_ms IS NULL OR last_probe_latency_ms >= 0),
    last_probe_kind text,
    last_probe_status text CHECK (last_probe_status IS NULL OR last_probe_status IN ('succeeded', 'failed', 'unavailable', 'uncertain')),
    last_probe_error_kind text,
    retired_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK ((status = 'retired') = (retired_at IS NOT NULL))
);

CREATE TABLE credential_mutations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id uuid NOT NULL REFERENCES users(id),
    action text NOT NULL,
    idempotency_key uuid NOT NULL,
    request_fingerprint bytea NOT NULL CHECK (octet_length(request_fingerprint) = 32),
    request_id text NOT NULL,
    credential_id uuid REFERENCES provider_credentials(id),
    result jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (actor_user_id, action, idempotency_key)
);

CREATE TABLE credential_models (
    credential_id uuid NOT NULL REFERENCES provider_credentials(id),
    model_id uuid NOT NULL REFERENCES models(id),
    priority integer NOT NULL DEFAULT 100,
    weight integer NOT NULL DEFAULT 100 CHECK (weight > 0),
    PRIMARY KEY (credential_id, model_id)
);

CREATE TABLE gateway_key_models (
    gateway_key_id uuid NOT NULL REFERENCES gateway_keys(id),
    model_id uuid NOT NULL REFERENCES models(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (gateway_key_id, model_id)
);

CREATE TABLE service_plans (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug text NOT NULL UNIQUE CHECK (slug ~ '^[a-z0-9][a-z0-9-]{1,62}[a-z0-9]$'),
    name text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    description text NOT NULL DEFAULT '' CHECK (char_length(description) <= 500),
    kind plan_kind NOT NULL,
    status service_plan_status NOT NULL DEFAULT 'active',
    current_version_id uuid,
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE service_plan_versions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    service_plan_id uuid NOT NULL REFERENCES service_plans(id),
    version integer NOT NULL CHECK (version > 0),
    token_quota bigint NOT NULL CHECK (token_quota > 0),
    validity_days integer NOT NULL CHECK (validity_days BETWEEN 1 AND 3650),
    concurrency_limit integer NOT NULL CHECK (concurrency_limit BETWEEN 1 AND 10000),
    rpm_limit integer CHECK (rpm_limit IS NULL OR rpm_limit > 0),
    tpm_limit bigint CHECK (tpm_limit IS NULL OR tpm_limit > 0),
    created_by uuid NOT NULL REFERENCES users(id),
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (service_plan_id, version),
    UNIQUE (id, service_plan_id)
);

CREATE TABLE service_plan_version_routes (
    service_plan_version_id uuid NOT NULL REFERENCES service_plan_versions(id),
    model_id uuid NOT NULL REFERENCES models(id),
    resource_pool_id uuid NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (service_plan_version_id, model_id),
    FOREIGN KEY (resource_pool_id, model_id) REFERENCES resource_pool_models(resource_pool_id, model_id)
);

ALTER TABLE service_plans
    ADD CONSTRAINT service_plans_current_version_fk
    FOREIGN KEY (current_version_id, id) REFERENCES service_plan_versions(id, service_plan_id);

CREATE TRIGGER service_plan_versions_immutable
BEFORE UPDATE OR DELETE ON service_plan_versions
FOR EACH ROW EXECUTE FUNCTION reject_immutable_record_mutation();

CREATE TRIGGER service_plan_version_routes_immutable
BEFORE UPDATE OR DELETE ON service_plan_version_routes
FOR EACH ROW EXECUTE FUNCTION reject_immutable_record_mutation();

CREATE TABLE service_plan_mutations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id uuid NOT NULL REFERENCES users(id),
    action text NOT NULL,
    idempotency_key uuid NOT NULL,
    request_fingerprint bytea NOT NULL CHECK (octet_length(request_fingerprint) = 32),
    request_id text NOT NULL,
    service_plan_id uuid REFERENCES service_plans(id),
    result jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (actor_user_id, action, idempotency_key)
);

CREATE TABLE subscriptions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id),
    service_plan_version_id uuid NOT NULL REFERENCES service_plan_versions(id),
    status subscription_status NOT NULL,
    granted_tokens bigint NOT NULL CHECK (granted_tokens > 0),
    starts_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    notes text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 500),
    assigned_by uuid NOT NULL REFERENCES users(id),
    suspended_at timestamptz,
    canceled_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (expires_at > starts_at),
    CHECK ((status = 'suspended') = (suspended_at IS NOT NULL)),
    CHECK ((status = 'canceled') = (canceled_at IS NOT NULL))
);

CREATE TABLE subscription_mutations (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    actor_user_id uuid NOT NULL REFERENCES users(id),
    action text NOT NULL,
    idempotency_key uuid NOT NULL,
    request_fingerprint bytea NOT NULL CHECK (octet_length(request_fingerprint) = 32),
    request_id text NOT NULL,
    subscription_id uuid REFERENCES subscriptions(id),
    result jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (actor_user_id, action, idempotency_key)
);

CREATE TABLE requests (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    idempotency_key text CHECK (idempotency_key IS NULL OR length(idempotency_key) BETWEEN 1 AND 200),
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    user_id uuid NOT NULL REFERENCES users(id),
    gateway_key_id uuid NOT NULL REFERENCES gateway_keys(id),
    model_id uuid NOT NULL REFERENCES models(id),
    subscription_id uuid NOT NULL REFERENCES subscriptions(id),
    resource_pool_id uuid NOT NULL REFERENCES resource_pools(id),
    price_version_id uuid NOT NULL REFERENCES model_price_versions(id),
    cost_currency text NOT NULL CHECK (cost_currency ~ '^[A-Z]{3}$'),
    input_rate_nanos_per_million bigint NOT NULL CHECK (input_rate_nanos_per_million BETWEEN 0 AND 1000000000000000),
    output_rate_nanos_per_million bigint NOT NULL CHECK (output_rate_nanos_per_million BETWEEN 0 AND 1000000000000000),
    input_cost_nanos bigint CHECK (input_cost_nanos IS NULL OR input_cost_nanos >= 0),
    output_cost_nanos bigint CHECK (output_cost_nanos IS NULL OR output_cost_nanos >= 0),
    total_cost_nanos bigint CHECK (total_cost_nanos IS NULL OR total_cost_nanos >= 0),
    status request_status NOT NULL,
    stream boolean NOT NULL,
    execution_id uuid,
    execution_generation bigint NOT NULL DEFAULT 0 CHECK (execution_generation >= 0),
    execution_claimed_at timestamptz,
    execution_heartbeat_at timestamptz,
    input_tokens bigint,
    output_tokens bigint,
    usage_source usage_source NOT NULL DEFAULT 'unknown',
    error_kind text,
    error_detail text,
    accepted_at timestamptz NOT NULL DEFAULT now(),
    completed_at timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (gateway_key_id, idempotency_key),
    UNIQUE (id, execution_id, execution_generation),
    CHECK ((input_cost_nanos IS NULL AND output_cost_nanos IS NULL AND total_cost_nanos IS NULL) OR
           (input_cost_nanos IS NOT NULL AND output_cost_nanos IS NOT NULL AND total_cost_nanos = input_cost_nanos + output_cost_nanos)),
    CHECK (
        (execution_generation = 0 AND execution_id IS NULL AND execution_claimed_at IS NULL AND execution_heartbeat_at IS NULL)
        OR
        (execution_generation > 0 AND execution_id IS NOT NULL AND execution_claimed_at IS NOT NULL AND execution_heartbeat_at IS NOT NULL)
    )
);

CREATE TABLE request_attempts (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    request_id uuid NOT NULL,
    execution_id uuid NOT NULL,
    execution_generation bigint NOT NULL CHECK (execution_generation > 0),
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
    input_tokens bigint CHECK (input_tokens IS NULL OR input_tokens >= 0),
    output_tokens bigint CHECK (output_tokens IS NULL OR output_tokens >= 0),
    usage_source usage_source NOT NULL DEFAULT 'unknown',
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (request_id, sequence),
    FOREIGN KEY (request_id, execution_id, execution_generation)
        REFERENCES requests(id, execution_id, execution_generation),
    CHECK ((input_tokens IS NULL) = (output_tokens IS NULL)),
    CHECK (input_tokens IS NOT NULL OR usage_source = 'unknown')
);

CREATE TABLE ledger_events (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id uuid NOT NULL REFERENCES users(id),
    subscription_id uuid NOT NULL REFERENCES subscriptions(id),
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
    subscription_id uuid NOT NULL REFERENCES subscriptions(id),
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
    request_id text,
    detail jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE response_records (
    id uuid PRIMARY KEY,
    request_id uuid UNIQUE REFERENCES requests(id) ON DELETE SET NULL,
    gateway_key_id uuid NOT NULL REFERENCES gateway_keys(id),
    previous_response_id uuid REFERENCES response_records(id) ON DELETE SET NULL,
    idempotency_key text,
    request_digest bytea,
    status response_status NOT NULL,
    background boolean NOT NULL DEFAULT false,
    encrypted_input bytea NOT NULL,
    encrypted_request bytea,
    encrypted_output bytea,
    encrypted_error bytea,
    execution_id uuid,
    execution_generation bigint NOT NULL DEFAULT 0 CHECK (execution_generation >= 0),
    execution_claimed_at timestamptz,
    execution_heartbeat_at timestamptz,
    cancel_requested_at timestamptz,
    completed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CHECK (NOT background OR encrypted_request IS NOT NULL),
    CHECK ((idempotency_key IS NULL AND request_digest IS NULL) OR
           (idempotency_key IS NOT NULL AND length(idempotency_key) BETWEEN 1 AND 200 AND octet_length(request_digest) = 32)),
    CHECK ((execution_id IS NULL AND execution_claimed_at IS NULL AND execution_heartbeat_at IS NULL) OR
           (background AND execution_id IS NOT NULL AND execution_generation > 0 AND execution_claimed_at IS NOT NULL AND execution_heartbeat_at IS NOT NULL))
);

CREATE UNIQUE INDEX users_email_lower_idx ON users (lower(email)) WHERE status <> 'deleted';
CREATE INDEX users_status_created_idx ON users (status, created_at DESC);
CREATE INDEX sessions_active_digest_idx ON sessions (token_digest) WHERE revoked_at IS NULL;
CREATE INDEX gateway_keys_active_digest_idx ON gateway_keys (secret_digest) WHERE revoked_at IS NULL;
CREATE INDEX gateway_key_models_model_idx ON gateway_key_models (model_id, gateway_key_id);
CREATE INDEX resource_pools_provider_status_idx ON resource_pools (provider_id, status, name);
CREATE INDEX provider_credentials_eligible_idx ON provider_credentials (resource_pool_id, status, cooldown_until);
CREATE INDEX subscriptions_applicable_idx ON subscriptions (user_id, status, starts_at, expires_at);
CREATE INDEX service_plan_versions_plan_idx ON service_plan_versions (service_plan_id, version DESC);
CREATE INDEX service_plan_routes_pool_idx ON service_plan_version_routes (resource_pool_id, model_id);
CREATE INDEX requests_user_created_idx ON requests (user_id, accepted_at DESC);
CREATE INDEX requests_accepted_idx ON requests (accepted_at DESC, id);
CREATE INDEX model_price_versions_effective_idx ON model_price_versions (model_id, effective_at DESC, created_at DESC);
CREATE INDEX requests_status_idx ON requests (status, updated_at);
CREATE INDEX requests_execution_recovery_idx ON requests (execution_heartbeat_at, id)
    WHERE status IN ('dispatching', 'streaming');
CREATE INDEX request_attempts_request_idx ON request_attempts (request_id, sequence);
CREATE INDEX request_attempts_credential_created_idx ON request_attempts (credential_id, created_at DESC, id);
CREATE INDEX ledger_events_user_created_idx ON ledger_events (user_id, created_at DESC);
CREATE INDEX ledger_events_subscription_created_idx ON ledger_events (subscription_id, created_at, id);
CREATE UNIQUE INDEX ledger_events_actor_source_event_idx ON ledger_events (created_by, source_event_id)
    WHERE source_event_id IS NOT NULL AND created_by IS NOT NULL;
CREATE UNIQUE INDEX ledger_events_system_source_event_idx ON ledger_events (source_event_id)
    WHERE source_event_id IS NOT NULL AND created_by IS NULL;
CREATE INDEX audit_events_created_idx ON audit_events (created_at DESC);
CREATE INDEX response_records_owner_created_idx ON response_records (gateway_key_id, created_at DESC, id);
CREATE UNIQUE INDEX response_records_owner_idempotency_idx ON response_records (gateway_key_id, idempotency_key)
    WHERE idempotency_key IS NOT NULL;
CREATE INDEX response_records_execution_idx ON response_records (status, execution_heartbeat_at, created_at, id)
    WHERE background = true AND status IN ('queued', 'in_progress');

-- +goose Down
DROP TABLE IF EXISTS response_records;
DROP TABLE IF EXISTS audit_events;
DROP TABLE IF EXISTS ledger_reservations;
DROP TABLE IF EXISTS ledger_events;
DROP TABLE IF EXISTS request_attempts;
DROP TABLE IF EXISTS requests;
DROP TABLE IF EXISTS subscription_mutations;
DROP TABLE IF EXISTS subscriptions;
DROP TABLE IF EXISTS service_plan_mutations;
DROP TRIGGER IF EXISTS service_plan_version_routes_immutable ON service_plan_version_routes;
DROP TABLE IF EXISTS service_plan_version_routes;
DROP TRIGGER IF EXISTS service_plan_versions_immutable ON service_plan_versions;
ALTER TABLE IF EXISTS service_plans DROP CONSTRAINT IF EXISTS service_plans_current_version_fk;
DROP TABLE IF EXISTS service_plan_versions;
DROP TABLE IF EXISTS service_plans;
DROP TABLE IF EXISTS credential_models;
DROP TABLE IF EXISTS credential_mutations;
DROP TABLE IF EXISTS provider_credentials;
DROP TRIGGER IF EXISTS model_price_versions_immutable ON model_price_versions;
DROP TABLE IF EXISTS model_price_mutations;
DROP TABLE IF EXISTS model_price_versions;
DROP FUNCTION IF EXISTS reject_immutable_record_mutation();
DROP TABLE IF EXISTS gateway_key_models;
DROP TABLE IF EXISTS gateway_key_mutations;
DROP TABLE IF EXISTS gateway_keys;
DROP TABLE IF EXISTS resource_pool_mutations;
DROP TABLE IF EXISTS resource_pool_models;
DROP TABLE IF EXISTS resource_pools;
DROP TABLE IF EXISTS models;
DROP TABLE IF EXISTS providers;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS member_mutations;
DROP TABLE IF EXISTS site_profile;
DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS system_state;
DROP TYPE IF EXISTS reservation_state;
DROP TYPE IF EXISTS plan_kind;
DROP TYPE IF EXISTS ledger_event_kind;
DROP TYPE IF EXISTS usage_source;
DROP TYPE IF EXISTS attempt_status;
DROP TYPE IF EXISTS response_status;
DROP TYPE IF EXISTS request_status;
DROP TYPE IF EXISTS subscription_status;
DROP TYPE IF EXISTS service_plan_status;
DROP TYPE IF EXISTS credential_status;
DROP TYPE IF EXISTS resource_pool_status;
DROP TYPE IF EXISTS user_role;
DROP TYPE IF EXISTS user_status;
