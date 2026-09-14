## Purpose

Даёт эксклюзивный флаг `--role` поверх существующих триггер-флагов
подсистем (`--remote-url`, `--rules-endpoint-path`), а также
универсальную поддержку env-переменных для всех флагов, чтобы можно
было раздать один общий конфиг на несколько специализированных по роли
инстансов avalanche (scrape-target / remote-writer / ruler), не
дублируя весь набор флагов в каждом деплое.

## ADDED Requirements

### Requirement: Флаг эксклюзивной роли инстанса
Система ДОЛЖНА (SHALL) предоставлять флаг `--role` с допустимыми
значениями `""` (default), `"scrape-target"`, `"remote-writer"`,
`"ruler"`. Непустое значение ДОЛЖНО (SHALL) ограничивать работу
процесса ТОЛЬКО соответствующей подсистемой, игнорируя триггер-флаги
остальных подсистем, даже если они заданы.

#### Scenario: Роль по умолчанию не меняет текущее поведение
- **WHEN** avalanche запущен без `--role` (значение по умолчанию `""`)
- **THEN** каждая подсистема работает как и до появления этого флага:
  `/metrics` всегда включён, remote-write работает при заданном
  `--remote-url`, генерация и раздача рулов — при непустом
  `--rules-endpoint-path`

#### Scenario: role=scrape-target игнорирует остальные триггеры
- **WHEN** `--role=scrape-target`, при этом `--remote-url` и
  `--rules-endpoint-path` заданы (например, оставлены от общего
  конфига)
- **THEN** работает только `/metrics` (+ `/health`); remote-write не
  запускается; рулы не генерируются и не раздаются

#### Scenario: role=remote-writer игнорирует остальные триггеры
- **WHEN** `--role=remote-writer`, `--remote-url` задан, при этом
  `--rules-endpoint-path` тоже задан
- **THEN** работает только remote-write (+ `/health`); `/metrics` не
  регистрируется; рулы не генерируются и не раздаются

#### Scenario: role=ruler игнорирует остальные триггеры
- **WHEN** `--role=ruler`, `--rules-endpoint-path` задан (или оставлен
  по умолчанию `/rules`), при этом `--remote-url` тоже задан
- **THEN** работает только раздача сгенерированных рулов (+ `/health`);
  `/metrics` не регистрируется; remote-write не запускается

### Requirement: Условное создание Collector
Если ни `--role=""` со `scrape-target`-подобным поведением
(`/metrics` включён), ни `--role=remote-writer` с заданным
`--remote-url` не приводят к необходимости генерировать серии, система
НЕ ДОЛЖНА (SHALL NOT) создавать и запускать `Collector`. В частности,
при `--role=ruler` `Collector` НЕ ДОЛЖЕН (SHALL NOT) создаваться.

#### Scenario: Ruler не создаёт Collector
- **WHEN** `--role=ruler`, `--rules-endpoint-path=/rules`
- **THEN** `Collector` не создаётся; процесс не тратит память/CPU на
  генерацию серий

### Requirement: Валидация роли при старте
Если `--role=remote-writer` задан без `--remote-url`, или
`--role=ruler` задан с пустым `--rules-endpoint-path`, система ДОЛЖНА
(SHALL) завершиться с понятной ошибкой валидации при старте, а не
запускаться в состоянии "ничего не делает, кроме `/health`".

#### Scenario: remote-writer без remote-url отклоняется
- **WHEN** avalanche запущен с `--role=remote-writer` и без
  `--remote-url`
- **THEN** процесс завершается с ошибкой валидации при старте

#### Scenario: ruler без rules-endpoint-path отклоняется
- **WHEN** avalanche запущен с `--role=ruler --rules-endpoint-path=""`
- **THEN** процесс завершается с ошибкой валидации при старте

### Requirement: /health всегда доступен
Эндпоинт `/health` ДОЛЖЕН (SHALL) оставаться доступным независимо от
значения `--role`.

#### Scenario: /health отвечает при любой роли
- **WHEN** avalanche запущен с любым допустимым значением `--role`
- **THEN** `GET /health` отвечает `200`

### Requirement: Env-переменные для всех флагов
Каждый флаг, регистрируемый в avalanche (включая флаги из
`metricsgen.NewConfigFromFlags` и `metricsgen.NewWriteConfigFromFlags`),
ДОЛЖЕН (SHALL) дополнительно читаться из переменной окружения
`AVALANCHE_<FLAG_NAME>` (имя флага в верхнем регистре, `-` заменены на
`_`). Явно заданный CLI-флаг ДОЛЖЕН (SHALL) переопределять значение из
переменной окружения. Встроенные флаги `--help`/`--version` НЕ ДОЛЖНЫ
(SHALL NOT) читаться из переменных окружения.

#### Scenario: Значение флага берётся из переменной окружения
- **WHEN** avalanche запущен с `AVALANCHE_SERIES_COUNT=42` и без
  явного `--series-count`
- **THEN** используется `series-count=42`

#### Scenario: Явный флаг переопределяет переменную окружения
- **WHEN** avalanche запущен с `AVALANCHE_SERIES_COUNT=42` и явным
  `--series-count=10`
- **THEN** используется `series-count=10`

#### Scenario: AVALANCHE_HELP/AVALANCHE_VERSION не действуют
- **WHEN** в окружении процесса заданы `AVALANCHE_HELP=1` или
  `AVALANCHE_VERSION=1`
- **THEN** avalanche запускается нормально, не показывая help/version и
  не завершаясь
