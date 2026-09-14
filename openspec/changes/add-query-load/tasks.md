## 1. Конфиг и флаги

- [ ] 1.1 Добавить `ConfigQuery` (по аналогии с `ConfigWrite`) с полями
      `URL *url.URL`, `InstantRate float64`, `RangeRate float64`,
      `Concurrency int`, `RangeWindow`, `RangeStep time.Duration`,
      `TemplateCount int`, и зарегистрировать флаги `--query-url`,
      `--query-instant-rate`, `--query-range-rate`,
      `--query-concurrency` (default `10`), `--query-range-window`
      (default `3600`), `--query-range-step` (default `60`),
      `--query-template-count` (default `20`) в
      `NewQueryConfigFromFlags`; проверить, что `avalanche --help`
      показывает все флаги с дефолтами.
- [ ] 1.2 Добавить `ConfigQuery.Validate()` (URL не пуст ⇒ валидный
      host/scheme, `Concurrency > 0`, `RangeWindow > 0`, `RangeStep > 0`,
      `TemplateCount > 0`); проверить unit-тестами некорректные
      комбинации.

## 2. Переиспользуемое перечисление метрик/labels

- [ ] 2.1 Если `add-rule-generation` уже реализован — переиспользовать его
      функцию перечисления `(metricName, metricType)` и ключей labels; если
      нет — реализовать эквивалентную функцию в рамках этого изменения
      (без побочных эффектов, детерминированную относительно `Config`);
      проверить unit-тестом стабильность между вызовами.

## 3. Генератор PromQL-шаблонов (querygen.go)

- [ ] 3.1 Создать `metricsgen/querygen.go` с функцией, строящей пул из
      `TemplateCount` PromQL-строк на основе списка метрик/labels (2.1),
      включающих формы: селектор с label matchers, агрегация (`sum`/
      `avg`/`max by (...)`), `rate()`/`increase()` для counter, бинарная
      операция между двумя разными метриками, subquery
      (`max_over_time(metric[window:step])`), `histogram_quantile` для
      histogram/native histogram; проверить unit-тестом присутствие
      каждой формы хотя бы один раз при достаточном `TemplateCount` и
      наличии соответствующих типов метрик в конфиге.
- [ ] 3.2 Для range queries отдельно подготовить параметры `start`/`end`/
      `step`, вычисляемые в момент отправки запроса (`end=now`,
      `start=now-RangeWindow`, `step=RangeStep`), а не при генерации
      шаблона (шаблон хранит только PromQL-строку и признак
      instant/range); проверить unit-тестом, что `start`/`end` двух
      последовательных отправок одного и того же шаблона различаются на
      ожидаемый интервал.

## 4. HTTP query-клиент (query.go)

- [ ] 4.1 Создать `metricsgen/query.go` с `Querier`, переиспользующим
      `buildTLSConfig`, `tenantRoundTripper`, `userAgentRoundTripper` из
      `write.go` для построения `http.Client`; проверить unit-тестом (или
      кодовым ревью), что TLS/tenant-конфигурация идентична используемой
      `Writer`.
- [ ] 4.2 Реализовать independent QPS-тикеры для instant и range запросов
      (`time.Ticker(time.Second / rate)` на каждый, только если
      соответствующий `rate > 0`) с общим семафором конкурентности
      (`chan struct{}, Concurrency`); неблокирующая постановка в очередь
      — при занятом семафоре тик логируется как пропущенный, а не
      блокирует генератор (design.md, Risk); проверить unit-тестом, что
      при `Concurrency=N` число одновременных in-flight запросов не
      превышает N под нагрузкой выше пропускной способности.
- [ ] 4.3 Отправлять GET/POST на `<query-url>/api/v1/query` (instant) и
      `<query-url>/api/v1/query_range` (range) с параметрами из 3.1/3.2;
      логировать ошибки (сетевые, не-200, `status != "success"` в теле
      ответа) со счётчиком, не прерывая работу; проверить
      unit/integration-тестом на моковом HTTP-сервере (200 success,
      500 error, timeout).
- [ ] 4.4 Подключить `Querier.Run`/`Querier.Stop` в `cmd/avalanche/avalanche.go`
      через `oklog/run.Group`, только если `--query-url` непусто, так
      чтобы ошибка/остановка querier'а не останавливала HTTP-сервер
      метрик, collector или remote-write; проверить integration-тестом,
      что при недоступном `--query-url` `/metrics` продолжает отвечать.

## 5. Интеграция и документация

- [ ] 5.1 Вручную запустить avalanche с `--query-url=http://<test-prom>:9090
      --query-instant-rate=5 --query-range-rate=2
      --query-concurrency=10` против тестового Prometheus и подтвердить
      в его метриках (`prometheus_http_requests_total` с
      `handler="/api/v1/query"`/`"/api/v1/query_range"`), что запросы
      приходят с ожидаемой частотой.
- [ ] 5.2 Обновить `README.md`, описав флаги query-нагрузки и пример
      использования вместе с существующими TLS/tenant флагами; проверить
      соответствие реальному выводу `--help`.
