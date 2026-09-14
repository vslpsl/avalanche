## Purpose

Даёт явные флаги ролей инстанса (scrape-target/remote-writer/querier/
ruler) поверх существующих url/path-триггеров подсистем и шардирование
генерации серий на уровне `series_id` внутри каждой метрики, чтобы
можно было специализировать и горизонтально распределять нагрузку между
множеством инстансов avalanche вместо одного универсального процесса.

## ADDED Requirements

### Requirement: Флаги ролей инстанса
Система ДОЛЖНА (SHALL) предоставлять четыре независимых bool-флага:
`--role-scrape-target`, `--role-remote-writer`, `--role-querier`,
`--role-ruler`, каждый по умолчанию `true`. Подсистема ДОЛЖНА (SHALL)
фактически работать только если её роль включена **и** её собственный
триггер-флаг задан (`--remote-url` для remote-writer, `--query-url` для
querier, `--rules-endpoint-path` для ruler; scrape-target триггерится
самим фактом запуска HTTP-сервера).

#### Scenario: Роли по умолчанию не меняют текущее поведение
- **WHEN** avalanche запущен без явного указания флагов ролей (все три
  используют значение по умолчанию `true`) и с произвольным набором
  url/path-флагов
- **THEN** активны ровно те подсистемы, что были бы активны без
  флагов ролей вообще (поведение идентично версии до этого изменения)

#### Scenario: Выключенная роль подавляет подсистему даже при заданном триггер-флаге
- **WHEN** `--role-querier=false`, но `--query-url` задан
- **THEN** query-клиент не запускается и HTTP-запросы к `--query-url` не
  отправляются

### Requirement: Условное создание Collector
Если ни `--role-scrape-target`, ни `--role-remote-writer` не равны
`true`, система НЕ ДОЛЖНА (SHALL NOT) создавать и запускать `Collector`
(генерация серий в памяти не происходит).

#### Scenario: Чистый querier не создаёт Collector
- **WHEN** `--role-scrape-target=false`, `--role-remote-writer=false`,
  `--role-querier=true`, `--query-url` задан
- **THEN** `Collector` не создаётся; процесс не тратит память/CPU на
  генерацию серий

### Requirement: Условная регистрация HTTP /metrics
Если `--role-scrape-target=false`, HTTP-хендлер `/metrics` НЕ ДОЛЖЕН
(SHALL NOT) регистрироваться. `/health` ДОЛЖЕН (SHALL) оставаться
доступным независимо от ролей. Если `--role-scrape-target=false`, но
`--role-remote-writer=true`, `Collector` ДОЛЖЕН (SHALL) всё равно
создаваться и работать — его данные использует только remote-write
клиент.

#### Scenario: Чистый remote-writer не отдаёт /metrics
- **WHEN** `--role-scrape-target=false`, `--role-remote-writer=true`,
  `--remote-url` задан
- **THEN** `/metrics` возвращает 404 (или не зарегистрирован), `/health`
  отвечает нормально, и remote-write отправляет данные на `--remote-url`

### Requirement: Флаги шардирования на уровне series_id
Система ДОЛЖНА (SHALL) предоставлять флаги `--shard-index` (целое число,
по умолчанию `0`) и `--shard-count` (целое число, по умолчанию `1`).
Каждая сконфигурированная метрика ДОЛЖНА (SHALL) существовать на каждом
шарде с одним и тем же именем; в рамках каждой метрики шард ДОЛЖЕН
(SHALL) инстанцировать только те `series_id` из диапазона `[0,
series_count)`, для которых `series_id % shard_count == shard_index`.

#### Scenario: Шард инстанцирует только свою долю series_id
- **WHEN** `--series-count=1000`, `--shard-count=10`, `--shard-index=3`
- **THEN** для каждой метрики этот инстанс инстанцирует ровно 100 серий
  — с `series_id` от значений, для которых `series_id % 10 == 3`

#### Scenario: Объединение всех шардов равно нешардированному набору
- **WHEN** запущены `shard-count` инстансов с `shard-index` от `0` до
  `shard-count-1` и идентичным остальным конфигом
- **THEN** объединение серий всех инстансов (по каждой метрике) равно
  полному набору `series_id` в `[0, series_count)`, без пропусков и
  пересечений

#### Scenario: Шардирование выключено по умолчанию
- **WHEN** avalanche запущен без `--shard-index`/`--shard-count`
  (значения по умолчанию `0`/`1`)
- **THEN** каждая метрика инстанцирует полный диапазон `series_id` — как
  и до этого изменения

### Requirement: Валидация параметров шардирования
Система ДОЛЖНА (SHALL) проверять при старте, что `--shard-count >= 1` и
`0 <= --shard-index < --shard-count`.

#### Scenario: Некорректный shard-index отклоняется
- **WHEN** avalanche запущен с `--shard-count=5` и `--shard-index=5`
- **THEN** валидация конфигурации завершается ошибкой (индекс вне
  диапазона `[0, shard-count)`)

### Requirement: Шардирование не затрагивает querier и ruler роли
`--shard-index`/`--shard-count` НЕ ДОЛЖНЫ (SHALL NOT) влиять на
поведение ролей `querier` и `ruler`: перечисление имён метрик/ключей
labels для построения запросов (`add-query-load`) и правил
(`add-rule-generation`) ДОЛЖНО (SHALL) всегда использовать полный
конфигурационный набор, независимо от значений `--shard-index`/
`--shard-count`.

#### Scenario: Querier не зависит от шардирования
- **WHEN** `--role-querier=true`, `--shard-count=10`, `--shard-index=3`
- **THEN** сгенерированные PromQL-шаблоны ссылаются на метрики так, как
  если бы `--shard-count` был равен `1`

### Requirement: Совместимость шардирования с churn'ом серий
Если активен механизм churn'а серий (`add-partial-series-churn`), он
ДОЛЖЕН (SHALL) применяться только к подмножеству `series_id`,
принадлежащему данному шарду (см. Requirement "Флаги шардирования на
уровне series_id"), так что целевой процент churn'а вычисляется
относительно доли шарда, а не относительно полного `--series-count`.

#### Scenario: Churn-percent считается от доли шарда
- **WHEN** `--series-count=1000`, `--shard-count=10`, `--shard-index=3`,
  `--partial-series-churn-percent=20`
- **THEN** целевое число churnящихся серий за окно вычисляется как 20%
  от 100 (доли этого шарда), то есть 20 серий, а не 20% от 1000
