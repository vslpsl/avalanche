## 1. Детерминизм значений

- [ ] 1.1 Добавить `Seed int64` в `metricsgen.Config`, зарегистрировать
      `--seed` (по умолчанию `0`) в `NewConfigFromFlags`; проверить, что
      `avalanche --help` показывает флаг с описанием сентинела `0`.
- [ ] 1.2 В `NewCollector` инициализировать `valGen` через
      `rand.NewSource(cfg.Seed)`, если `Seed != 0`, иначе как раньше
      (`time.Now().UnixNano()`); проверить unit-тестом, что два запуска с
      одинаковым ненулевым seed дают идентичную последовательность
      значений `cycleValues`.

## 2. Экспорт списка сгенерированных метрик/labels

- [ ] 2.1 Вынести формирование имени метрики (уже используемую в
      `recreateMetrics` логику `avalanche_<type>_metric_<...>`) в
      переиспользуемую функцию/метод, возвращающую список
      `(metricName, metricType)` для текущего конфига без побочных
      эффектов; проверить unit-тестом, что список стабилен между двумя
      вызовами с одинаковым конфигом.
- [ ] 2.2 Аналогично вынести формирование ключей labels
      (`label_key_<...>_<idx>` + const labels) в переиспользуемую
      функцию; проверить unit-тестом стабильность набора ключей.

## 3. Генератор файла правил

- [ ] 3.1 Добавить флаги `RulesOutputPath string`,
      `RecordingRuleCount int`, `AlertingRuleCount int`,
      `RuleGroupSize int` (default `10`), `RuleEvalInterval int` (сек,
      default `60`) в `Config`/`NewConfigFromFlags`; проверить, что
      `avalanche --help` показывает все пять флагов с дефолтами.
- [ ] 3.2 Создать `metricsgen/rulesgen.go` с функцией, строящей
      `rulefmt.RuleGroups` по списку метрик/labels (2.1, 2.2) и
      счётчикам `RecordingRuleCount`/`AlertingRuleCount`, с шаблонами
      выражений по типу метрики (design.md, Decision 3); проверить
      unit-тестом, что для gauge/counter/classic-histogram/native-
      histogram/summary генерируется ожидаемый вид `expr`.
- [ ] 3.3 Ограничить фактическое число recording rules числом доступных
      уникальных метрик-семейств (не генерировать дублирующиеся правила
      на одну и ту же метрику, если запрошено больше правил, чем
      метрик); проверить unit-тестом с `--recording-rule-count`, большим
      числа сконфигурированных метрик.
- [ ] 3.4 Реализовать распределение сгенерированных правил по группам
      размером не более `RuleGroupSize`, с `interval: RuleEvalInterval`
      на каждую группу; проверить unit-тестом разбиение 25 правил при
      `RuleGroupSize=10` на группы 10/10/5.
- [ ] 3.5 Сериализовать `rulefmt.RuleGroups` в YAML и записать по пути
      `RulesOutputPath`, вызывать из `main()` до старта HTTP-сервера,
      только если `RulesOutputPath != ""`; проверить unit/integration-
      тестом, что при пустом `RulesOutputPath` файл не создаётся, а при
      заданном — создаётся валидный YAML, проходящий
      `rulefmt.Parse`/`rulefmt.ParseFile`.

## 4. Alerting rules

- [ ] 4.1 Реализовать генерацию alerting rule поверх выбранного
      recording-выражения (или метрики напрямую) с пороговым условием,
      `for`, непустыми `labels`/`annotations` (design.md, Decision 3);
      проверить unit-тестом наличие всех обязательных полей в каждом
      сгенерированном alerting rule.

## 5. Интеграция и документация

- [ ] 5.1 Вручную запустить avalanche с
      `--rules-output-path=/tmp/rules.yml --recording-rule-count=10
      --alerting-rule-count=5`, подключить файл в тестовый Prometheus
      (`rule_files:`), сделать reload и убедиться, что все правила
      успешно загружаются (нет ошибок в `/api/v1/rules` или логах
      Prometheus).
- [ ] 5.2 Обновить `README.md`, описав новые флаги
      (`--seed`, `--rules-output-path`, `--recording-rule-count`,
      `--alerting-rule-count`, `--rule-group-size`,
      `--rule-eval-interval`) и пример использования с `rule_files:` в
      целевом Prometheus; проверить, что документация соответствует
      реальному выводу `--help`.
