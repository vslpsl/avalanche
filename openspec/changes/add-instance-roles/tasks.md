## 1. Флаг роли

- [x] 1.1 Добавить в `cmd/avalanche/avalanche.go` флаг `--role`
      (enum, `""`/`scrape-target`/`remote-writer`/`ruler`, default
      `""`); проверить, что `avalanche --help` показывает флаг с
      допустимыми значениями.
- [x] 1.2 После `cfg.Validate()`/`writeCfg.Validate()` добавить
      валидацию роли: `role=remote-writer` без `--remote-url` и
      `role=ruler` с пустым `--rules-endpoint-path` — ошибка при
      старте (`kingpin.FatalUsage`); проверить вручную оба случая.
- [x] 1.3 Вычислить `doScrape`/`doRemoteWrite`/`doRules`/
      `needsCollector` по роли (см. design.md, Decision 3); при
      `role==""` — точное совпадение со старым поведением.
- [x] 1.4 Создавать `Collector`/регистрировать в `reg`/запускать в
      `run.Group` только при `needsCollector`; проверить вручную, что
      `--role=ruler` не создаёт `Collector` (лог "initializing
      avalanche" появляется, но без выделения серий).
- [x] 1.5 Регистрировать `/metrics` только при `doScrape`; `/health`
      всегда; рулы — только при `doRules`; remote-write — только при
      `doRemoteWrite`; проверить вручную все 4 роли (`""`,
      `scrape-target`, `remote-writer`, `ruler`) на `/metrics`,
      `/health`, `/rules`, факт отправки remote-write.

## 2. Env-переменные для всех флагов

- [x] 2.1 В начале `main()` вызвать `kingpin.CommandLine.Name =
      "avalanche"` и `kingpin.CommandLine.DefaultEnvars()`, сразу
      следом — `kingpin.CommandLine.HelpFlag.NoEnvar()` и
      `kingpin.CommandLine.VersionFlag.NoEnvar()` (не через
      package-level `kingpin.HelpFlag`/`kingpin.VersionFlag` — паника
      на nil, см. design.md Decision 2); проверить, что `avalanche
      --help` не показывает регресса в списке флагов.
- [x] 2.2 Проверить вручную: `AVALANCHE_SERIES_COUNT=42 avalanche
      --role=scrape-target` действительно отдаёт 42 серии на метрику
      без единой правки в `metricsgen/serve.go`; явный
      `--series-count` при этом переопределяет env.

## 3. Документация

- [x] 3.1 Обновить `README.md`: описать `--role` (четыре значения,
      default `""`), общий механизm `AVALANCHE_<FLAG_NAME>` env-
      переопределения для всех флагов (с оговоркой про
      `--const-label`/`\n`-разделитель), пример типового деплоя (общий
      `ConfigMap` + несколько Deployment'ов, отличающихся только
      `--role`/`AVALANCHE_ROLE`); проверить соответствие реальному
      выводу `--help`.
