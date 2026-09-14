## Purpose

Вводит новый, полностью независимый от `--series-interval` механизм
плавного частичного ("partial") churn'а серий за окно, управляющий
отдельной меткой `churn_generation`, чтобы можно было моделировать
постепенную ротацию доли серий (например, процент серий, churnящихся за
длительность блока TSDB), не затрагивая существующее поведение
`--series-interval`.

## ADDED Requirements

### Requirement: Независимость от --series-interval
Существующее поведение `--series-interval` (полный churn всех серий на
каждом тике, управление меткой `cycle_id`) НЕ ДОЛЖНО (SHALL NOT)
изменяться этой capability. Новый механизм ДОЛЖЕН (SHALL) использовать
собственные флаги и собственную метку, не пересекаясь с `cycle_id`.

#### Scenario: series-interval работает как раньше
- **WHEN** `--series-interval` задан, а
  `--partial-series-churn-percent` не задан (значение по умолчанию `0`)
- **THEN** поведение полностью идентично поведению до этого изменения:
  каждый тик `--series-interval` churnит 100% серий через `cycle_id`,
  метка `churn_generation` не добавляется

### Requirement: Флаг длительности окна
Система ДОЛЖНА (SHALL) предоставлять флаг
`--partial-series-churn-interval` (целое число секунд, по умолчанию
`7200` = 2 часа), задающий окно, за которое ДОЛЖЕН быть достигнут
целевой `--partial-series-churn-percent`.

#### Scenario: Значение по умолчанию соответствует block duration TSDB
- **WHEN** `--partial-series-churn-interval` не задан явно
- **THEN** используется значение `7200` секунд (2 часа)

### Requirement: Флаг целевого процента churn'а — триггер механизма
Система ДОЛЖНА (SHALL) предоставлять флаг
`--partial-series-churn-percent` (целое число, 0-100, по умолчанию `0`),
задающий целевую долю активных серий, которая ДОЛЖНА (SHALL) получить
новую identity (через `churn_generation`) суммарно за одно окно.
Значение `0` ДОЛЖНО (SHALL) полностью отключать механизм: метка
`churn_generation` не добавляется ни к одной серии.

#### Scenario: Нулевой процент — механизм выключен
- **WHEN** `--partial-series-churn-percent=0` (по умолчанию)
- **THEN** метка `churn_generation` не добавляется ни к одной серии,
  независимо от значений `--partial-series-churn-interval`/
  `--partial-series-churn-step`

### Requirement: Флаг интервала шага churn'а
Система ДОЛЖНА (SHALL) предоставлять флаг `--partial-series-churn-step`
(целое число секунд, по умолчанию `30`) — самостоятельный флаг,
ОТДЕЛЬНЫЙ от `--series-interval`, задающий длительность одного шага
применения churn'а внутри окна `--partial-series-churn-interval`.

#### Scenario: Значения по умолчанию дают разумную гранулярность
- **WHEN** `--partial-series-churn-percent=20` задан, а
  `--partial-series-churn-interval`/`--partial-series-churn-step` не
  заданы явно (используются дефолты `7200`/`30`)
- **THEN** окно длится 7200 секунд и состоит из 240 шагов по 30 секунд
  (`7200/30`); суммарно к концу каждого окна churnится примерно 20%
  активных серий, небольшими порциями на каждом шаге

### Requirement: Кумулятивная цель churn'а на шаге
На каждом шаге `i` (от `1` до `steps = partial_series_churn_interval /
partial_series_churn_step`) внутри окна система ДОЛЖНА (SHALL)
вычислять кумулятивную цель числа churnящихся серий как
`round(window_target_count * i / steps)`, где `window_target_count =
floor(series_count * partial_series_churn_percent / 100)`, и ДОЛЖНА
(SHALL) churnить (через `churn_generation`) ровно дельту между этой
целью и числом серий, уже churnувшихся в текущем окне.

#### Scenario: Равномерная дельта на каждом шаге
- **WHEN** `series_count=1000`, `partial_series_churn_percent=20`,
  `partial_series_churn_step=300`, `partial_series_churn_interval=1200`
  (`steps=4`, `window_target_count=200`)
- **THEN** на шагах 1, 2, 3, 4 кумулятивные цели составляют 50, 100, 150
  и 200 серий соответственно

#### Scenario: Округление не накапливает ошибку к концу окна
- **WHEN** `series_count=1000`, `partial_series_churn_percent=17`,
  `partial_series_churn_step=200`,
  `partial_series_churn_interval=1200` (`steps=6`,
  `window_target_count=170`)
- **THEN** на последнем шаге (`i=6`) кумулятивная цель равна ровно 170

### Requirement: Отдельная метка identity для нового механизма
Новый механизм ДОЛЖЕН (SHALL) выражать churn identity через отдельную
метку `churn_generation` (счётчик поколения на слот), а не через
`cycle_id`. Метка `churn_generation` ДОЛЖНА (SHALL) присутствовать на
сериях только когда `--partial-series-churn-percent > 0`.

#### Scenario: Метка появляется только при активном механизме
- **WHEN** `--partial-series-churn-percent=20`
- **THEN** сгенерированные серии содержат метку `churn_generation` в
  дополнение ко всем существующим меткам, включая `cycle_id`
  (управляемый независимо через `--series-interval`, если он задан)

### Requirement: Случайный отбор без повторов в пределах окна
Слоты серий, churnящиеся (через `churn_generation`) в течение одного
окна, ДОЛЖНЫ (SHALL) выбираться случайно и равномерно из активных
слотов, без повторов: серия, уже churnившаяся в текущем окне, НЕ ДОЛЖНА
(SHALL NOT) быть выбрана повторно до начала следующего окна.

#### Scenario: Один и тот же слот не churnится дважды за окно
- **WHEN** окно содержит несколько шагов churn'а
- **THEN** каждый конкретный слот серии встречается в объединении
  churn-наборов всех шагов этого окна не более одного раза

#### Scenario: Новое окно начинает отбор заново
- **WHEN** текущее окно завершается и начинается следующее
- **THEN** набор кандидатов на churn перетасовывается заново, и слоты,
  churnившиеся в предыдущем окне, снова доступны для выбора

### Requirement: Нечурнутые серии остаются стабильными
Слоты серий, не выбранные для churn'а (через `churn_generation`) ни на
одном из шагов текущего окна, НЕ ДОЛЖНЫ (SHALL NOT) менять значение
`churn_generation` в течение этого окна и ДОЛЖНЫ (SHALL) продолжать
обновляться как обычно на тиках `--value-interval`.

#### Scenario: Незатронутый слот сохраняет churn_generation в пределах окна
- **WHEN** слот серии не был выбран для churn'а ни на одном шаге
  текущего окна
- **THEN** значение его `churn_generation` не меняется в течение всего
  окна, а значение серии продолжает обновляться согласно настроенному
  `--value-interval`

### Requirement: Валидация параметров нового механизма
Система ДОЛЖНА (SHALL) проверять при старте, что:
`--partial-series-churn-percent` находится в диапазоне `[0, 100]`;
`--partial-series-churn-interval > 0`; `--partial-series-churn-step >
0`; и `--partial-series-churn-interval` кратен
`--partial-series-churn-step` без остатка (и не меньше него). При
нарушении любого из этих условий система ДОЛЖНА (SHALL) завершать
валидацию конфигурации ошибкой с описанием проблемы.

#### Scenario: Значение процента вне диапазона отклоняется
- **WHEN** avalanche запущен с `--partial-series-churn-percent=150`
- **THEN** валидация конфигурации завершается ошибкой

#### Scenario: Некратный шаг отклоняется
- **WHEN** avalanche запущен с `--partial-series-churn-interval=1000` и
  `--partial-series-churn-step=300`
- **THEN** валидация конфигурации завершается ошибкой, так как 1000 не
  делится на 300 без остатка

### Requirement: Независимость от других механизмов
`--partial-series-churn-interval`, `--partial-series-churn-percent` и
`--partial-series-churn-step` ДОЛЖНЫ (SHALL) влиять только на метку
`churn_generation`. Они НЕ ДОЛЖНЫ (SHALL NOT) изменять поведение
`--series-interval`, `--value-interval`, `--metric-interval` или
`--series-operation-mode`; и наоборот — `--series-interval` НЕ ДОЛЖЕН
(SHALL NOT) влиять на `churn_generation`.

#### Scenario: Оба механизма работают одновременно независимо
- **WHEN** заданы одновременно `--series-interval=60` и
  `--partial-series-churn-percent=20`
- **THEN** `cycle_id` продолжает полностью churnиться каждые 60 секунд
  (через `--series-interval`), а `churn_generation` независимо
  накапливает частичный churn к каждым 7200 секундам (дефолт окна) —
  оба механизма не влияют друг на друга
