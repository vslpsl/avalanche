## Purpose

Генерирует управляемую по QPS и concurrency query-нагрузку (instant и
range queries) на отдельный Prometheus-совместимый query API, используя
широкий набор PromQL-шаблонов поверх детерминированно сгенерированных
метрик avalanche, чтобы воспроизвести нагрузку на query engine
(дашборды/API-клиенты), а не только на приём данных.

## ADDED Requirements

### Requirement: Флаг адреса query API
Система ДОЛЖНА (SHALL) предоставлять флаг `--query-url` (URL, по
умолчанию пустой). Если флаг пуст, query-нагрузка НЕ ДОЛЖНА (SHALL NOT)
генерироваться и поведение avalanche НЕ ДОЛЖНО (SHALL NOT) отличаться от
текущего. Значение `--query-url` НЕ ДОЛЖНО (SHALL NOT) зависеть от
`--remote-url` — это независимый целевой адрес.

#### Scenario: Флаг не задан — query-нагрузка выключена
- **WHEN** avalanche запущен без `--query-url`
- **THEN** HTTP-запросы к query API не отправляются

#### Scenario: Query-url независим от remote-url
- **WHEN** заданы одновременно разные `--remote-url` и `--query-url`
- **THEN** remote-write запросы идут на `--remote-url`, а query-запросы —
  на `--query-url`, без взаимного влияния

### Requirement: Instant queries с управляемым QPS
Система ДОЛЖНА (SHALL) предоставлять флаг `--query-instant-rate` (число с
плавающей точкой, QPS, по умолчанию `0`). При положительном значении
avalanche ДОЛЖЕН (SHALL) отправлять запросы к `<query-url>/api/v1/query` с
частотой, соответствующей заданному QPS.

#### Scenario: Instant queries выключены по умолчанию
- **WHEN** avalanche запущен с `--query-url`, но без
  `--query-instant-rate` (по умолчанию `0`)
- **THEN** запросы к `/api/v1/query` не отправляются

#### Scenario: Instant queries отправляются с заданным QPS
- **WHEN** `--query-url` задан и `--query-instant-rate=10`
- **THEN** в установившемся режиме avalanche отправляет в среднем 10
  запросов в секунду к `/api/v1/query`

### Requirement: Range queries с управляемым QPS и параметрами окна
Система ДОЛЖНА (SHALL) предоставлять флаги `--query-range-rate` (QPS, по
умолчанию `0`), `--query-range-window` (секунды, по умолчанию `3600`) и
`--query-range-step` (секунды, по умолчанию `60`). При положительном
`--query-range-rate` avalanche ДОЛЖЕН (SHALL) отправлять запросы к
`<query-url>/api/v1/query_range` с частотой, соответствующей заданному
QPS, где `start = now - query_range_window`, `end = now`, `step =
query_range_step`.

#### Scenario: Range queries выключены по умолчанию
- **WHEN** avalanche запущен с `--query-url`, но без
  `--query-range-rate` (по умолчанию `0`)
- **THEN** запросы к `/api/v1/query_range` не отправляются

#### Scenario: Range queries используют заданное окно и шаг
- **WHEN** `--query-range-rate=5`, `--query-range-window=1800`,
  `--query-range-step=30`
- **THEN** каждый range-запрос покрывает интервал длиной 1800 секунд от
  текущего момента с шагом 30 секунд

### Requirement: Ограничение конкурентности запросов
Система ДОЛЖНА (SHALL) предоставлять флаг `--query-concurrency` (целое
число, по умолчанию `10`), ограничивающий число одновременно выполняемых
HTTP-запросов к query API (instant и range суммарно).

#### Scenario: Число одновременных запросов не превышает лимит
- **WHEN** `--query-concurrency=10` и суммарный QPS
  (`--query-instant-rate` + `--query-range-rate`) достаточно высок, чтобы
  создать очередь
- **THEN** число одновременно выполняемых HTTP-запросов к query API не
  превышает 10

### Requirement: Широкий набор PromQL-шаблонов
Система ДОЛЖНА (SHALL) генерировать запросы, используя набор PromQL-форм
шире, чем в recording rules, включающий как минимум: селектор с label
matchers, агрегацию (`sum`/`avg`/`max` `by (...)`), `rate()`/`increase()`
для counter-метрик, бинарную операцию между двумя разными
сгенерированными метриками, subquery (например,
`max_over_time(<metric>[1h:5m])`) и `histogram_quantile` для
histogram-метрик. Используемые имена метрик и ключи labels ДОЛЖНЫ (SHALL)
браться из детерминированного списка, введённого в capability
`rule-generation` (Requirement "Стабильность имён метрик и ключей
labels").

#### Scenario: Генерируются разные формы запросов
- **WHEN** сгенерирован пул из `--query-template-count` PromQL-шаблонов
- **THEN** среди них присутствует как минимум один запрос каждой формы
  из перечисленных (селектор, агрегация, rate/increase, бинарная
  операция между метриками, subquery, histogram_quantile — при наличии
  соответствующих типов метрик в конфиге)

### Requirement: Размер пула шаблонов
Система ДОЛЖНА (SHALL) предоставлять флаг `--query-template-count`
(целое число, по умолчанию `20`), задающий число различных PromQL-запросов,
подготавливаемых один раз при старте. Отправляемые запросы ДОЛЖНЫ (SHALL)
выбираться из этого пула (случайно или по кругу).

#### Scenario: Число подготовленных шаблонов соответствует флагу
- **WHEN** `--query-template-count=20`
- **THEN** при старте подготавливается ровно 20 различных PromQL-запросов
  (или меньше, если детерминированный список метрик не позволяет
  построить столько различных валидных запросов)

### Requirement: Переиспользование существующей TLS- и tenant-конфигурации
Query-клиент ДОЛЖЕН (SHALL) использовать те же флаги TLS
(`--tls-client-insecure`, `--tls-client-cert-file`,
`--tls-client-key-file`, `--tls-ca-cert-file`) и tenant-заголовка
(`--remote-tenant`, `--remote-tenant-header`), что и remote-write клиент,
без отдельного набора флагов.

#### Scenario: TLS-настройки применяются к query-клиенту
- **WHEN** заданы `--tls-client-cert-file`/`--tls-client-key-file` и
  `--query-url` использует `https://`
- **THEN** query-клиент использует тот же TLS-сертификат для соединения
  с `--query-url`, что и remote-write клиент для `--remote-url`

### Requirement: Независимость от других механизмов нагрузки
`--query-url` и связанные флаги ДОЛЖНЫ (SHALL) не влиять на поведение
scrape (`/metrics`), remote-write или генерацию правил
(`add-rule-generation`). Ошибки query-запросов НЕ ДОЛЖНЫ (SHALL NOT)
останавливать генерацию метрик, scrape-сервер или remote-write.

#### Scenario: Ошибка query API не останавливает остальную нагрузку
- **WHEN** `--query-url` указывает на недоступный адрес
- **THEN** scrape `/metrics` и (если настроен) remote-write продолжают
  работать нормально, а query-клиент логирует ошибки и продолжает
  попытки согласно заданному QPS
