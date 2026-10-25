-- Схема кредитного конвейера. Деньги — bigint в целых рублях.

CREATE TABLE companies (
    id                uuid PRIMARY KEY,
    inn               varchar(12)  NOT NULL UNIQUE,
    ogrn              varchar(15)  NOT NULL DEFAULT '',
    name              text         NOT NULL,
    full_name         text         NOT NULL DEFAULT '',
    legal_form        varchar(8)   NOT NULL,
    status            varchar(20)  NOT NULL DEFAULT 'ACTIVE',
    registration_date date,
    director_name     text         NOT NULL DEFAULT '',
    okved_code        varchar(10)  NOT NULL DEFAULT '',
    okved_name        text         NOT NULL DEFAULT '',
    address           text         NOT NULL DEFAULT '',
    created_at        timestamptz  NOT NULL DEFAULT now()
);

CREATE TABLE users (
    id            uuid PRIMARY KEY,
    email         text        NOT NULL UNIQUE,
    password_hash text        NOT NULL,
    full_name     text        NOT NULL,
    position      text        NOT NULL DEFAULT '',
    role          varchar(20) NOT NULL CHECK (role IN ('CLIENT', 'UNDERWRITER')),
    company_id    uuid REFERENCES companies (id),
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE SEQUENCE application_number_seq START 1001;

CREATE TABLE applications (
    id                        uuid PRIMARY KEY,
    number                    bigint      NOT NULL UNIQUE DEFAULT nextval('application_number_seq'),
    company_id                uuid        NOT NULL REFERENCES companies (id),
    applicant_id              uuid        NOT NULL REFERENCES users (id),
    product_code              varchar(32) NOT NULL,
    purpose                   varchar(32) NOT NULL,
    purpose_comment           text        NOT NULL DEFAULT '',
    amount                    bigint      NOT NULL DEFAULT 0 CHECK (amount >= 0),
    term_months               int         NOT NULL DEFAULT 0 CHECK (term_months >= 0),
    annual_revenue            bigint      NOT NULL DEFAULT 0 CHECK (annual_revenue >= 0),
    existing_monthly_payments bigint      NOT NULL DEFAULT 0 CHECK (existing_monthly_payments >= 0),
    has_collateral            boolean     NOT NULL DEFAULT false,
    collateral_description    text        NOT NULL DEFAULT '',
    contact_phone             varchar(32) NOT NULL DEFAULT '',
    bki_consent               boolean     NOT NULL DEFAULT false,
    status                    varchar(32) NOT NULL,
    status_changed_at         timestamptz NOT NULL DEFAULT now(),
    version                   int         NOT NULL DEFAULT 1,
    assigned_underwriter_id   uuid REFERENCES users (id),
    offer_amount              bigint,
    offer_term_months         int,
    offer_rate                numeric(5, 2),
    offer_monthly_payment     bigint,
    decision_reason           text        NOT NULL DEFAULT '',
    client_message            text        NOT NULL DEFAULT '',
    sign_code                 varchar(8)  NOT NULL DEFAULT '',
    signed_at                 timestamptz,
    issued_at                 timestamptz,
    account_number            varchar(32) NOT NULL DEFAULT '',
    submitted_at              timestamptz,
    created_at                timestamptz NOT NULL DEFAULT now(),
    updated_at                timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX applications_company_idx ON applications (company_id, created_at DESC);
CREATE INDEX applications_status_idx ON applications (status, status_changed_at);

-- Журнал событий: история статусов + аудит действий. Только добавление.
CREATE TABLE application_events (
    id               bigserial PRIMARY KEY,
    application_id   uuid        NOT NULL REFERENCES applications (id),
    from_status      varchar(32),
    to_status        varchar(32),
    event_type       varchar(48) NOT NULL,
    actor_type       varchar(16) NOT NULL,
    actor_id         uuid,
    actor_name       text        NOT NULL DEFAULT '',
    message          text        NOT NULL DEFAULT '',
    internal_comment text        NOT NULL DEFAULT '',
    visibility       varchar(8)  NOT NULL DEFAULT 'ALL' CHECK (visibility IN ('ALL', 'BANK')),
    payload          jsonb,
    created_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX application_events_app_idx ON application_events (application_id, id);

CREATE TABLE enrichment_results (
    id             bigserial PRIMARY KEY,
    application_id uuid        NOT NULL REFERENCES applications (id),
    source         varchar(16) NOT NULL,
    status         varchar(16) NOT NULL,
    data           jsonb,
    error          text        NOT NULL DEFAULT '',
    duration_ms    int         NOT NULL DEFAULT 0,
    attempts       int         NOT NULL DEFAULT 0,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX enrichment_results_app_idx ON enrichment_results (application_id, source, id DESC);

CREATE TABLE scoring_results (
    id             bigserial PRIMARY KEY,
    application_id uuid          NOT NULL REFERENCES applications (id),
    model_version  varchar(32)   NOT NULL,
    score          int           NOT NULL,
    grade          varchar(2)    NOT NULL,
    pd             numeric(6, 4) NOT NULL,
    factors        jsonb         NOT NULL,
    stop_factors   jsonb         NOT NULL,
    recommendation jsonb         NOT NULL,
    created_at     timestamptz   NOT NULL DEFAULT now()
);
CREATE INDEX scoring_results_app_idx ON scoring_results (application_id, id DESC);

CREATE TABLE document_requests (
    id             uuid PRIMARY KEY,
    application_id uuid        NOT NULL REFERENCES applications (id),
    doc_type       varchar(32) NOT NULL,
    title          text        NOT NULL,
    comment        text        NOT NULL DEFAULT '',
    status         varchar(16) NOT NULL DEFAULT 'REQUESTED' CHECK (status IN ('REQUESTED', 'UPLOADED')),
    requested_by   uuid REFERENCES users (id),
    created_at     timestamptz NOT NULL DEFAULT now(),
    fulfilled_at   timestamptz
);
CREATE INDEX document_requests_app_idx ON document_requests (application_id);

CREATE TABLE documents (
    id             uuid PRIMARY KEY,
    application_id uuid         NOT NULL REFERENCES applications (id),
    request_id     uuid REFERENCES document_requests (id),
    file_name      text         NOT NULL,
    content_type   varchar(128) NOT NULL,
    size_bytes     bigint       NOT NULL,
    content        bytea        NOT NULL,
    uploaded_by    uuid REFERENCES users (id),
    created_at     timestamptz  NOT NULL DEFAULT now()
);
CREATE INDEX documents_app_idx ON documents (application_id);

-- Очередь фоновых задач (transactional outbox): задача создаётся в той же транзакции,
-- что и смена статуса, поэтому не теряется при сбое процесса.
CREATE TABLE jobs (
    id             bigserial PRIMARY KEY,
    application_id uuid        NOT NULL REFERENCES applications (id),
    kind           varchar(32) NOT NULL,
    status         varchar(16) NOT NULL DEFAULT 'PENDING' CHECK (status IN ('PENDING', 'RUNNING', 'DONE', 'FAILED')),
    attempts       int         NOT NULL DEFAULT 0,
    max_attempts   int         NOT NULL DEFAULT 5,
    run_at         timestamptz NOT NULL DEFAULT now(),
    locked_at      timestamptz,
    last_error     text        NOT NULL DEFAULT '',
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX jobs_pending_idx ON jobs (run_at) WHERE status = 'PENDING';
