## Why

avalanche сейчас умеет создавать нагрузку через scrape/remote-write
(запись) и (в `add-rule-generation`) через rule evaluation engine, но не
умеет создавать нагрузку через query API — а именно чтение через
`/api/v1/query` и `/api/v1/query_range` часто является основным
источником деградации на реальных инсталляциях (дашборды, API-клиенты).
Сейчас нет способа сгенерировать управляемую по QPS/concurrency
query-нагрузку на целевой Prometheus-совместимый сервис.

## What Changes

- Добавляется новый query-клиент (аналог существующего remote-write
  клиента), который шлёт HTTP-запросы к Prometheus-совместимому query
  API по отдельному от remote-write целевому адресу.
- Флаг `--query-url` (URL, по умолчанию пусто = функциональность
  выключена) — базовый адрес query API, независимый от `--remote-url`.
- Поддерживаются оба вида запросов:
  - instant queries (`/api/v1/query`) — флаг `--query-instant-rate`
    (QPS, по умолчанию `0`).
  - range queries (`/api/v1/query_range`) — флаг `--query-range-rate`
    (QPS, по умолчанию `0`), с параметрами окна и шага
    (`--query-range-window`, `--query-range-step`).
- Интенсивность нагрузки регулируется через QPS
  (`--query-instant-rate`/`--query-range-rate`) и ограничение
  одновременных запросов `--query-concurrency` — по аналогии с
  `--remote-concurrency-limit` в существующем remote-write клиенте.
- Генерируется отдельный, более широкий набор PromQL-шаблонов, чем в
  `add-rule-generation` (селекторы с label matchers, агрегации,
  rate/increase, бинарные операции между двумя метриками, subquery,
  `histogram_quantile`), — не переиспользует шаблоны recording rules
  напрямую, но переиспользует детерминированный список имён метрик и
  ключей labels, введённый в `add-rule-generation` (Requirement
  "Стабильность имён метрик и ключей labels").
- Переиспользуется существующая TLS-конфигурация
  (`--tls-client-*`/`--tls-ca-cert-file`) и tenant-заголовки
  (`--remote-tenant`/`--remote-tenant-header`) — для query-клиента не
  вводится отдельный набор TLS/tenant флагов.
- Функциональность полностью опциональна: при `--query-url=""` (по
  умолчанию) поведение avalanche не меняется.

## Capabilities

### New Capabilities
- `query-load`: генерирует управляемую по QPS и concurrency
  query-нагрузку (instant + range) на отдельный Prometheus-совместимый
  query API, с широким набором PromQL-шаблонов поверх детерминированного
  списка сгенерированных метрик/labels.

### Modified Capabilities
(нет)

## Impact

- Код: новый файл `metricsgen/querygen.go` (генерация PromQL-шаблонов) и
  `metricsgen/query.go` (HTTP query-клиент, QPS/concurrency),
  `cmd/avalanche/avalanche.go` (проброс новых флагов, запуск querier
  через `oklog/run.Group` параллельно с collector'ом и remote-write).
- Зависимость (implementation-level, не capability-level): переиспользует
  функции перечисления имён метрик/ключей labels, вводимые в
  `add-rule-generation` (tasks 2.1/2.2 того изменения) и его контракт
  стабильности имён. Если `add-rule-generation` ещё не реализован к
  моменту реализации этого изменения, эквивалентная функция
  перечисления должна быть добавлена как часть этого изменения (не
  блокирующая зависимость, но переиспользование предпочтительно во
  избежание дублирования).
- Сеть: новый исходящий HTTP-трафик к `--query-url`, независимый от
  `--remote-url`.
- Нет изменений в существующих флагах генерации метрик, scrape или
  remote-write.
