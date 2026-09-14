## 1. Детерминизм значений

- [x] 1.1 Добавить `Seed int64` в `metricsgen.Config`, зарегистрировать
      `--seed` (по умолчанию `0`) в `NewConfigFromFlags`; проверить, что
      `avalanche --help` показывает флаг с описанием сентинела `0`.
- [x] 1.2 В `NewCollector` инициализировать `valGen` через
      `rand.NewSource(cfg.Seed)`, если `Seed != 0`, иначе как раньше
      (`time.Now().UnixNano()`); проверить unit-тестом, что два запуска с
      одинаковым ненулевым seed дают идентичную последовательность
      значений `cycleValues`.

## 2. Экспорт списка сгенерированных метрик/labels

- [x] 2.1 Вынести формирование имени метрики (уже используемую в
      `recreateMetrics` логику `avalanche_<type>_metric_<...>`) в
      переиспользуемую функцию/метод, возвращающую список
      `(metricName, metricType)` для текущего конфига без побочных
      эффектов; проверить unit-тестом, что список стабилен между двумя
      вызовами с одинаковым конфигом.
- [x] 2.2 Аналогично вынести формирование ключей labels
      (`label_key_<...>_<idx>` + const labels) в переиспользуемую
      функцию; проверить unit-тестом стабильность набора ключей.

## 3. Генератор правил и HTTP-эндпоинт

- [x] 3.1 Добавить флаги `RulesEndpointPath string` (default `/rules`),
      `RecordingRuleCount int`, `AlertingRuleCount int`,
      `RuleGroupSize int` (default `10`), `RuleEvalInterval int` (сек,
      default `60`) в `Config`/`NewConfigFromFlags`; проверить, что
      `avalanche --help` показывает все пять флагов с дефолтами (включая
      `/rules` у `RulesEndpointPath`).
- [x] 3.2 Создать `metricsgen/rulesgen.go` с собственными Go-структурами
      `ruleGroups`/`ruleGroup`/`rule` (зеркалирующими формат Prometheus
      rule files, БЕЗ импорта `github.com/prometheus/prometheus/model/rulefmt`
      — тянет полный PromQL engine и ~30 indirect-модулей, включая
      `k8s.io/client-go`, ради пары структур; см. design.md Risk) и
      функцией, строящей их по списку метрик/labels (2.1, 2.2) и
      счётчикам `RecordingRuleCount`/`AlertingRuleCount`, с шаблонами
      выражений по типу метрики (design.md, Decision 3); проверить
      unit-тестом, что для gauge/counter/classic-histogram/native-
      histogram/summary генерируется ожидаемый вид `expr`.
- [x] 3.3 Ограничить фактическое число recording rules числом доступных
      уникальных метрик-семейств (не генерировать дублирующиеся правила
      на одну и ту же метрику, если запрошено больше правил, чем
      метрик); проверить unit-тестом с `--recording-rule-count`, большим
      числа сконфигурированных метрик.
- [x] 3.4 Реализовать распределение сгенерированных правил по группам
      размером не более `RuleGroupSize`, с `interval: RuleEvalInterval`
      на каждую группу; проверить unit-тестом разбиение 25 правил при
      `RuleGroupSize=10` на группы 10/10/5.
- [x] 3.5 Сериализовать `ruleGroups` в YAML (через `go.yaml.in/yaml/v3`,
      переведённый из indirect в прямую зависимость) один раз в `main()`
      (до начала обслуживания `/metrics`) и, только если
      `RulesEndpointPath != ""` (включая дефолт `/rules`), зарегистрировать
      `http.HandleFunc(RulesEndpointPath, ...)`, отдающий этот
      закешированный `[]byte`-снапшот с заголовком
      `Content-Type: application/yaml`; проверить unit/integration-тестом,
      что при `RulesEndpointPath=""` хендлер не регистрируется, а при
      дефолтном/заданном пути — `GET` по этому пути возвращает валидный
      YAML (проверяется round-trip через собственную `ruleGroups` —
      `promql/parser` тоже отпал: транзитивно тянет `storage`→`tsdb` и
      те же тяжёлые зависимости, что и `rulefmt`, см. design.md Risk),
      и тело ответа идентично между несколькими последовательными
      запросами.
- [x] 3.6 Проверить unit-тестом, что шаблоны выражений (3.2) и генератор
      правил (2.1, 2.2) — чистые функции только от `Config`: не
      принимают и не используют ничего похожего на "число реплик"/
      "число подов", и что ни одно сгенерированное выражение не содержит
      подстрок `pod`, `instance` или `job` в качестве группировки/фильтра
      (независимость генерации правил от количества реплик с одинаковым
      конфигом, design.md Decision 5).

## 4. Alerting rules

- [x] 4.1 Реализовать генерацию alerting rule поверх выбранного
      recording-выражения (или метрики напрямую) с пороговым условием,
      `for`, непустыми `labels`/`annotations` (design.md, Decision 3);
      проверить unit-тестом наличие всех обязательных полей в каждом
      сгенерированном alerting rule.

## 5. Интеграция и документация

- [x] 5.1 Вручную запустить avalanche с
      `--recording-rule-count=10 --alerting-rule-count=5` (без явного
      `--rules-endpoint-path` — проверить, что дефолтный `/rules`
      действительно работает), скачать `curl localhost:9001/rules` в
      файл, подключить его в тестовый Prometheus (`rule_files:`),
      сделать reload и убедиться, что все правила успешно загружаются
      (нет ошибок в `/api/v1/rules` или логах Prometheus).
- [x] 5.2 Обновить `README.md`, описав новые флаги
      (`--seed`, `--rules-endpoint-path`, `--recording-rule-count`,
      `--alerting-rule-count`, `--rule-group-size`,
      `--rule-eval-interval`) и пример получения правил через
      `curl <avalanche>/rules` с последующим подключением к целевому
      Prometheus через `rule_files:`; проверить, что документация
      соответствует реальному выводу `--help`.
