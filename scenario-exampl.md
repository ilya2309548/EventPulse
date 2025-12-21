# Сценарий авто‑миттейшена по алерту «CPU > 90%»

## Источник алерта
- **Система**: Prometheus/Alertmanager
- **Правило**: `cpu_high`
- **Объект**: сервис `svc-a`
- **Payload**: `alert_id`, `source=prometheus`, `severity=warning`, `labels: service=svc-a, threshold=90%, ts`
- **Действие**: отправка алерта в **Ingest**

## Приём и публикация
1. **Ingest**:
   - Валидирует и нормализует данные
   - Пишет запись в журнал/outbox
   - Публикует событие `alert.raised` в **Broker**
   - **Ключи**: `alert_id`, `trace_id`

## Принятие решения правилами
2. **Rule Engine** потребляет `alert.raised` и применяет правила:
   - **Открытие инцидента**: `incident.opened` (`incident_id`, `service=svc-a`, `severity=warning`)
   - **Запрос действия**: `action.requested` (`action_id`, `type=scale_up`, `target=svc-a`, `delta=+1`)
   - **Запрос уведомления**: `notification.requested` (`incident_id`, `channel=slack`, `template=incident_opened`)
   - **Идемпотентность**: по ключам `alert_id`/`incident_id`/`action_id`

## Учёт состояния инцидента
3. **Incident Store API**:
   - Потребляет `incident.opened`
   - Обновляет **IncidentDB**: статус `open`, связка `alert_id` ↔ `incident_id`
   - Публикует статусное событие через outbox (при необходимости)

## Исполнение действия
4. **Action Runner** потребляет `action.requested`:
   - Выполняет runbook: вызов Cloud API для масштабирования `svc-a`
   - Использует Circuit Breaker, таймауты, ретраи
   - **При успехе**: публикует `action.completed`
   - **При неудаче**: публикует `action.failed`

## Завершение/обновление инцидента
5. **Incident Store API** потребляет `action.completed`:
   - Обновляет статус инцидента: `resolved`
   - Пишет историю (кто, когда, что изменил)
   - Публикует статусное событие (опционально)

6. Если было `action.failed`:
   - Статус: `failed` или `mitigating` (в зависимости от политики)
   - Может инициировать компенсацию/альтернативное действие через **Rule Engine** (перезапуск, откат скейла)

## Уведомления
7. **Notification Service** потребляет `notification.requested`:
   - Формирует сообщение, отправляет в Slack/Email/Webhook
   - Публикует `notification.delivered`

8. **Incident Store API** потребляет `notification.delivered`:
   - Логирует факт доставки
   - Обновляет поле `last_notified_at`

## Наблюдаемость и конфигурация
- **Трейсинг/метрики/логи**: все сервисы пишут в **OpenTelemetry**
- **Корреляция**: по `trace_id`, `alert_id`, `incident_id`
- **Конфигурации/секреты**: из централизованного хранилища (Vault/OPA/KMS)

---

## Пример цепочки событий