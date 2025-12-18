# EventPulse: архитектурная схема (хореография событий)

Ниже — схема компонентов, потоков событий и отмеченные базовые паттерны распределённой системы. Модель построена как чистая хореография: нет центрального оркестратора, все реакции — через события.

```mermaid
flowchart LR
  %% Core services
  Ingest[Telemetry Ingest]:::svc
  Broker[Event Broker: Kafka/NATS]:::broker
  Rules[Rule Engine]:::svc
  Action[Action Runner]:::svc
  IncidentAPI[Incident Store API]:::svc
  IncidentDB[(Incident DB: Postgres)]:::store
  Notify[Notification Service]:::svc

  %% External systems
  Prometheus[Prometheus / Alertmanager]:::ext
  Chat[ChatOps / Chat]:::ext
  Cloud[Cluster / Cloud APIs]:::ext

  %% Observability & Config
  OTel[OpenTelemetry]:::infra
  Conf[Config & Secrets: Vault/OPA/KMS]:::infra

  %% Topics (logical as nodes)
  A1{{alert.raised}}:::broker
  I1{{incident.opened}}:::broker
  AR{{action.requested}}:::broker
  AC{{action.completed}}:::broker
  AF{{action.failed}}:::broker
  NR{{notification.requested}}:::broker
  ND{{notification.delivered}}:::broker

  %% External flow into Ingest
  Prometheus --> Ingest
  Ingest --> Broker
  Broker --> A1

  %% Rule Engine reacts to alert.raised
  A1 --> Rules
  Rules --> I1
  Rules --> AR
  Rules --> NR

  %% Incident Store API consumes events
  I1 --> IncidentAPI
  AC --> IncidentAPI
  AF --> IncidentAPI
  ND --> IncidentAPI
  IncidentAPI --> IncidentDB
  IncidentAPI --> Broker

  %% Action Runner executes actions
  AR --> Action
  Action --> Cloud
  Action --> AC
  Action --> AF

  %% Notification service
  NR --> Notify
  Notify --> ND
  ND --> Chat

  %% Observability and config across services
  Ingest --- OTel
  Rules --- OTel
  Action --- OTel
  IncidentAPI --- OTel
  Notify --- OTel

  Ingest --- Conf
  Rules --- Conf
  Action --- Conf
  IncidentAPI --- Conf
  Notify --- Conf

  %% Styles
  classDef svc fill:#eef,stroke:#335,stroke-width:1px,color:#000;
  classDef store fill:#efe,stroke:#353,stroke-width:1px,color:#000;
  classDef broker fill:#fee,stroke:#533,stroke-width:1px,color:#000;
  classDef ext fill:#ddd,stroke:#555,stroke-width:1px,color:#000;
  classDef infra fill:#fff,stroke:#aaa,stroke-width:1px,color:#000;
```

## Компоненты
- Telemetry Ingest
  - Принимает алерты из Prometheus/Alertmanager, нормализует, пишет в outbox-таблицу и публикует `alert.raised`.
- Rule Engine
  - Подписывается на `alert.raised`. По правилам решает: открыть инцидент (`incident.opened`), запросить действие (`action.requested`), отправить уведомление (`notification.requested`).
- Action Runner
  - Подписывается на `action.requested`. Выполняет runbook/скрипт с таймаутами/ретраями/схемой Circuit Breaker. Публикует `action.completed` или `action.failed`.
- Incident Store API
  - Подписывается на `incident.opened`, `action.completed`, `action.failed`, `notification.delivered`. Обновляет состояние в БД. Имеет REST/gRPC. При изменениях использует transactional outbox и публикует статусные события.
- Notification Service
  - Подписывается на `notification.requested`, доставляет уведомления (Slack/Email/Webhooks), публикует `notification.delivered`.
- Event Broker
  - Темы: `alert.raised`, `incident.opened`, `action.requested`, `action.completed`, `action.failed`, `notification.requested`, `notification.delivered`.
- Observability
  - OpenTelemetry для трейсинга/метрик/логов, корреляция по `trace_id` и `incident_id`.
- Config & Secrets
  - Централизованная конфигурация, хранение секретов (Vault/KMS), политики (OPA).

## Базовые паттерны, отражённые на схеме
- Хореография (Event-driven): все переходы инициируются событиями, нет центрального оркестратора.
- Transactional Outbox + CDC:
  - Ingest и Incident Store API при записи в БД публикуют события атомарно.
- Идемпотентность обработчиков:
  - Rule Engine, Notification Service и Incident Store API используют ключи `alert_id`/`incident_id` для безопасных повторов.
- Circuit Breaker + Таймауты + Retry (с джиттером):
  - На вызовах внешних систем из Action Runner.
- Bulkhead (изоляция ресурсов):
  - Раздельные пулы для Rule Engine, Action Runner, Notification Service.
- Backpressure:
  - Очереди брокера ограничивают скорость потребления; у сервисов — контролируемая конкуренция.
- Разделение данных:
  - Хранилище инцидентов принадлежит Incident Store API; остальные — статусы через события.
- Наблюдаемость:
  - Корреляция по событиям и трейсинг по пути Alert → Incident → Action → Notification.
- Health checks:
  - Readiness/Liveness для каждого сервиса.
- Безопасность:
  - mTLS между сервисами, авторизация API, секрет-менеджмент.

## Минимальные темы и ключи идемпотентности
- Темы:
  - `alert.raised`, `incident.opened`, `action.requested`, `action.completed`, `action.failed`, `notification.requested`, `notification.delivered`.
- Ключи:
  - `alert_id`, `incident_id`, `action_id` (композиция: источник + ts + правило).
  - Deduplication по ключу в Rule Engine и Notification Service.

## Потоки и статусы инцидента (пример)
- Статусы: `open` → `mitigating` → `resolved` / `failed`.
- Переходы:
  - `incident.opened` → `open`
  - `action.requested` → `mitigating`
  - `action.completed` → `resolved`
  - `action.failed` → `failed` (или компенсационные действия по правилам)

Эта схема покрывает базовые паттерны, оставаясь компактной и чисто событийно-хореографической.