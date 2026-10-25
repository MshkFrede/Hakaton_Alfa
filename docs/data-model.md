# Модель данных

Схема — [`migrations/001_init.sql`](../migrations/001_init.sql). Денежные суммы хранятся в `bigint` целыми рублями: без плавающей точки и ошибок округления.

```mermaid
erDiagram
    companies ||--o{ users : "представители"
    companies ||--o{ applications : "подаёт"
    users ||--o{ applications : "заявитель"
    users |o--o{ applications : "специалист"
    applications ||--o{ application_events : "журнал"
    applications ||--o{ enrichment_results : "данные источников"
    applications ||--o{ scoring_results : "оценки"
    applications ||--o{ document_requests : "запросы банка"
    document_requests ||--o{ documents : "файлы"
    applications ||--o{ documents : ""
    applications ||--o{ jobs : "фоновые задачи"

    companies {
        uuid id PK
        varchar inn UK "10 или 12 цифр"
        varchar ogrn
        text name
        varchar legal_form "ООО | ИП | АО"
        varchar status "по ЕГРЮЛ"
        date registration_date
        text director_name
        varchar okved_code
    }
    users {
        uuid id PK
        text email UK
        text password_hash "PBKDF2-SHA256"
        varchar role "CLIENT | UNDERWRITER"
        uuid company_id FK "null для банка"
    }
    applications {
        uuid id PK
        bigint number UK "КЗ-001001"
        uuid company_id FK
        uuid applicant_id FK
        varchar product_code
        varchar purpose "задача бизнеса"
        bigint amount
        int term_months
        bigint annual_revenue "заявлено"
        bigint existing_monthly_payments
        boolean has_collateral
        boolean bki_consent
        varchar status
        int version "оптимистичная блокировка"
        uuid assigned_underwriter_id FK
        bigint offer_amount "условия банка"
        int offer_term_months
        numeric offer_rate
        bigint offer_monthly_payment
        text decision_reason "только для банка"
        text client_message "для клиента"
        varchar sign_code
        timestamptz submitted_at
        timestamptz signed_at
        timestamptz issued_at
    }
    application_events {
        bigserial id PK
        uuid application_id FK
        varchar from_status
        varchar to_status
        varchar event_type
        varchar actor_type "CLIENT | UNDERWRITER | SYSTEM"
        uuid actor_id
        text message "видно клиенту"
        text internal_comment "только банку"
        varchar visibility "ALL | BANK"
        jsonb payload
        timestamptz created_at
    }
    enrichment_results {
        bigserial id PK
        uuid application_id FK
        varchar source "EGRUL | BKI | FNS"
        varchar status "OK | ERROR"
        jsonb data "снимок ответа"
        int duration_ms
        int attempts
    }
    scoring_results {
        bigserial id PK
        uuid application_id FK
        varchar model_version
        int score
        varchar grade "A-D"
        numeric pd
        jsonb factors
        jsonb stop_factors
        jsonb recommendation "решение политики"
    }
    document_requests {
        uuid id PK
        uuid application_id FK
        varchar doc_type
        text comment
        varchar status "REQUESTED | UPLOADED"
    }
    documents {
        uuid id PK
        uuid application_id FK
        uuid request_id FK
        text file_name
        bigint size_bytes
        bytea content
    }
    jobs {
        bigserial id PK
        uuid application_id FK
        varchar kind "ENRICH | SCORE | DISBURSE"
        varchar status "PENDING | RUNNING | DONE | FAILED"
        int attempts
        timestamptz run_at
        text last_error
    }
```

![Модель данных](img/data-model.svg)

## Принципы

- **Журнал событий только дополняется.** `application_events` — история статусов и аудит действий: кто, когда, что сделал, с каким комментарием. Поле `visibility` отделяет то, что видно клиенту, от внутренних записей банка.
- **Снимки данных источников.** `enrichment_results.data` хранит ответ источника на момент решения. Решение можно воспроизвести и объяснить даже после того, как данные в реестре изменятся. При повторной обработке добавляется новая запись, старые не перезаписываются.
- **Версионирование модели.** `scoring_results.model_version` фиксирует, какой версией модели получена оценка, а `recommendation` — что предложила политика. Это нужно для аудита и сравнения версий модели.
- **Условия оффера — отдельно от запроса.** `amount`/`term_months` — что просил клиент, `offer_*` — что предложил банк. Клиент видит оба значения, изменённые зачёркнуты.
- **Каталог продуктов — в коде** (`internal/domain/products.go`). Для MVP это прозрачнее, условия версионируются вместе с кодом и тестами. В промышленной версии — справочник в БД с историей.

## Индексы

| Индекс | Для чего |
|---|---|
| `applications (company_id, created_at DESC)` | Список заявок клиента |
| `applications (status, status_changed_at)` | Очереди специалиста |
| `application_events (application_id, id)` | История заявки |
| `enrichment_results (application_id, source, id DESC)` | Последний ответ каждого источника |
| `jobs (run_at) WHERE status = 'PENDING'` | Частичный индекс для выборки задач воркером |
