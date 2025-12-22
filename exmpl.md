# EventPulse: архитектурная схема (хореография событий) — обновлённая без Notifications/ChatOps/OTel/Config

```mermaid
flowchart LR
  %% Core services
  Ingest[Telemetry Ingest]:::svc
  Broker[Event Broker: Kafka/NATS]:::broker
  Rules[Rule Engine]:::svc
  Action[Action Runner]:::svc
  IncidentAPI[Incident Store API]:::svc

  %% External systems
  Prometheus[Prometheus / Alertmanager]:::ext
  Cloud[Cluster / Cloud APIs]:::ext

  %% Databases (cylinders) with table lists
  IngestDB[(Ingest DB: alerts, outbox_events)]:::store
  RulesDB[(Rule DB: rules, inbox_events, decisions_log, outbox_events)]:::store
  IncidentDB[(Incident DB: incidents, incident_events, actions, inbox_events, outbox_events)]:::store
  ActionDB[(Action Runner DB: action_exec, inbox_events, outbox_events)]:::store

  %% Topics (logical as nodes)
  A1{{alert.raised}}:::broker
  I1{{incident.opened}}:::broker
  AR{{action.requested}}:::broker
  AC{{action.completed}}:::broker
  AF{{action.failed}}:::broker

  %% Flows
  Prometheus --> Ingest
  Ingest --> Broker
  Broker --> A1

  A1 --> Rules
  Rules --> I1
  Rules --> AR

  I1 --> IncidentAPI
  AC --> IncidentAPI
  AF --> IncidentAPI
  IncidentAPI --> Broker

  AR --> Action
  Action --> Cloud
  Action --> AC
  Action --> AF

  %% DB attachments
  Ingest --- IngestDB
  Rules --- RulesDB
  IncidentAPI --- IncidentDB
  Action --- ActionDB

  %% Styles
  classDef svc fill:#eef,stroke:#335,stroke-width:1px,color:#000;
  classDef store fill:#efe,stroke:#353,stroke-width:1px,color:#000;
  classDef broker fill:#fee,stroke:#533,stroke-width:1px,color:#000;
  classDef ext fill:#ddd,stroke:#555,stroke-width:1px,color:#000;
```

## Таблицы в БД (сводно)
- Ingest DB: `alerts`, `outbox_events`
- Rule DB: `rules`, `inbox_events`, `decisions_log`, `outbox_events`
- Incident DB: `incidents`, `incident_events`, `actions`, `inbox_events`, `outbox_events`
- Action Runner DB: `action_exec`, `inbox_events`, `outbox_events`