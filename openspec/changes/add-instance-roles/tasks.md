## 1. Флаги ролей

- [ ] 1.1 Добавить в `cmd/avalanche/avalanche.go` флаги
      `--role-scrape-target`, `--role-remote-writer`, `--role-querier`,
      `--role-ruler` (bool, default `true` у всех); проверить, что
      `avalanche --help` показывает все четыре с дефолтом `true`.
- [ ] 1.2 Реализовать условие `needsCollector := roleScrapeTarget ||
      roleRemoteWriter`; создавать `Collector`/регистрировать его в
      `reg` только если `needsCollector`; проверить unit/integration-
      тестом, что при обеих ролях `false` `Collector` не создаётся (нет
      выделения памяти под метрики).
- [ ] 1.3 Регистрировать HTTP-хендлер `/metrics` только если
      `roleScrapeTarget=true`; `/health` регистрировать всегда;
      проверить integration-тестом, что при `roleScrapeTarget=false`
      `/metrics` возвращает 404, а `/health` — 200.
- [ ] 1.4 Гейтить запуск remote-write (`roleRemoteWriter &&
      writeCfg.URL != nil`), querier из `add-query-load`
      (`roleQuerier && queryCfg.URL != ""`), ruler из
      `add-rule-generation` (`roleRuler && rulesCfg.OutputPath != ""`);
      проверить unit-тестами все 4 комбинации роль=true/false ×
      триггер-флаг задан/не задан для одной из подсистем (например,
      remote-writer).

## 2. Флаги шардирования

- [ ] 2.1 Добавить `ShardIndex int`, `ShardCount int` в
      `metricsgen.Config` (default `0`/`1`), зарегистрировать
      `--shard-index`/`--shard-count`; добавить в `Config.Validate()`
      проверку `ShardCount >= 1` и `0 <= ShardIndex < ShardCount`;
      проверить unit-тестами валидные и невалидные комбинации.
- [ ] 2.2 Реализовать хелпер `ownsSeriesID(idx, shardIndex, shardCount
      int) bool` (`idx % shardCount == shardIndex`); проверить
      unit-тестом, что объединение `ownsSeriesID` для всех
      `shardIndex` от `0` до `shardCount-1` покрывает `[0,
      seriesCount)` без пропусков и пересечений.

## 3. Партиционирование генерации серий

- [ ] 3.1 Применить `ownsSeriesID` в `cycleValues` и `deleteValues`
      (`metricsgen/serve.go`) — итерировать только по `series_id`,
      принадлежащим шарду; проверить unit-тестом, что при
      `ShardCount=10`, `ShardIndex=3`, `SeriesCount=1000` для каждой
      метрики инстанцируется ровно 100 серий с ожидаемыми `series_id`.
- [ ] 3.2 Проверить регрессионным тестом, что при `ShardCount=1`
      (default) поведение `cycleValues`/`deleteValues` идентично
      текущему (полный диапазон `series_id`).
- [ ] 3.3 Если `add-partial-series-churn` к этому моменту реализован —
      применить `ownsSeriesID` к инициализации `churnPool`/
      `churnGeneration` и к вычислению `blockTargetCount` (доля от
      `shard_owned_series_count`, не от полного `SeriesCount`); проверить
      unit-тестом сценарий "Churn-percent считается от доли шарда" из
      specs (1000 серий, 10 шардов, 20% churn ⇒ 20 серий на шард, не
      200).

## 4. Интеграция и документация

- [ ] 4.1 Вручную запустить 3 инстанса avalanche с одинаковым конфигом
      (`--series-count=300`) и `--shard-count=3`,
      `--shard-index=0/1/2` соответственно; собрать `/metrics` со всех
      трёх и подтвердить, что объединение `series_id` по каждой метрике
      равно `[0, 300)` без пересечений.
- [ ] 4.2 Вручную запустить инстанс с `--role-scrape-target=false
      --role-remote-writer=true --remote-url=...` и убедиться, что
      `/metrics` недоступен, а remote-write продолжает отправлять
      данные (проверить через логи целевого приёмника или его
      `/api/v1/query` со значением `up`/счётчиком принятых samples).
- [ ] 4.3 Обновить `README.md`, описав флаги ролей и шардирования, с
      примером типового шардированного деплоя (несколько
      scrape-target/remote-writer шардов + один querier + один ruler
      инстанс, использующие общий конфиг); проверить соответствие
      реальному выводу `--help`.
