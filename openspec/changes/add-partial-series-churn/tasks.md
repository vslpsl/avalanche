## 1. Конфиг и флаги

- [x] 1.1 Добавить `PartialSeriesChurnInterval int` (default `7200`),
      `PartialSeriesChurnPercent int` (default `0`),
      `PartialSeriesChurnStep int` (default `30`) в `metricsgen.Config`,
      зарегистрировать `--partial-series-churn-interval`,
      `--partial-series-churn-percent`, `--partial-series-churn-step`
      в `NewConfigFromFlags`; убедиться, что `--series-interval`
      регистрируется и работает без каких-либо изменений; проверить,
      что `avalanche --help` показывает три новых флага с дефолтами
      `7200`/`0`/`30`.
- [x] 1.2 Добавить в `Config.Validate()` проверки: `0 <=
      PartialSeriesChurnPercent <= 100`;
      `PartialSeriesChurnInterval > 0`; `PartialSeriesChurnStep > 0`;
      `PartialSeriesChurnInterval >= PartialSeriesChurnStep И
      PartialSeriesChurnInterval % PartialSeriesChurnStep == 0`;
      проверить unit-тестами каждое нарушение по отдельности, а также
      что дефолтные значения (`7200`/`30`) сами по себе проходят
      валидацию.

## 2. Состояние нового механизма

- [x] 2.1 Добавить `metricState.churnGeneration []int32` (размером
      `SeriesCount`, инициализируется нулями в `Run()`, только если
      `PartialSeriesChurnPercent > 0`); НЕ трогать
      `metricState.seriesCycle` и `handleSeriesTicks`; проверить
      unit-тестом, что существующие тесты `--series-interval`/
      `cycle_id` проходят без изменений.
- [x] 2.2 Обновить `seriesLabels`, чтобы добавлять метку
      `churn_generation` из `churnGeneration[idx]`, только если
      `PartialSeriesChurnPercent > 0`; проверить unit-тестом отсутствие
      этой метки при `PartialSeriesChurnPercent=0` и её наличие при
      `PartialSeriesChurnPercent>0`.
- [x] 2.3 Добавить `churnPool []int32` (единичная перестановка `[0,
      SeriesCount)`) и `churnedSoFar int`, `stepInWindow int` в
      `Collector`/`metricState`, инициализируемые только при активном
      механизме; проверить unit-тестом, что после инициализации
      `churnPool` содержит каждый индекс ровно один раз.
- [x] 2.4 Реализовать хелпер полной перетасовки `churnPool`
      (Fisher-Yates), используемый при инициализации и на границе
      каждого окна; проверить unit-тестом, что результат остаётся
      перестановкой.

## 3. Новый тикер и пошаговая логика

- [x] 3.1 Создать `churnTick *time.Ticker` с периодом
      `PartialSeriesChurnStep`, только если
      `PartialSeriesChurnPercent > 0`; запустить отдельный обработчик
      (`handleChurnTicks`), независимый от `handleSeriesTicks`;
      проверить unit-тестом, что при `PartialSeriesChurnPercent=0`
      тикер не создаётся вовсе.
- [x] 3.2 В `handleChurnTicks`: инкрементировать `stepInWindow`,
      вычислить `windowTargetCount = floor(SeriesCount *
      PartialSeriesChurnPercent / 100)`, `steps =
      PartialSeriesChurnInterval / PartialSeriesChurnStep`,
      `cumulativeTarget = round(windowTargetCount * stepInWindow /
      steps)`, дельту `cumulativeTarget - churnedSoFar`, churnить
      (`churnGeneration[idx]++`, обновить значения) слоты
      `churnPool[churnedSoFar:cumulativeTarget]`, обновить
      `churnedSoFar = cumulativeTarget`; проверить unit-тестом с
      `SeriesCount=1000`, `PartialSeriesChurnPercent=20`,
      `PartialSeriesChurnStep=300`,
      `PartialSeriesChurnInterval=1200` (`steps=4`), что на шагах 1-4
      churnится по 50 серий на каждом шаге.
- [x] 3.3 При `stepInWindow == steps` (граница окна): после применения
      дельты сбросить `stepInWindow = 0`, `churnedSoFar = 0`, полностью
      перетасовать `churnPool`; проверить unit-тестом точное достижение
      `windowTargetCount` к последнему шагу, включая случай неровного
      деления (`PartialSeriesChurnPercent=17`, `steps=6` → ровно 170 из
      1000).
- [x] 3.4 Проверить unit-тестом отсутствие повторного churn'а одного
      слота (по `churn_generation`) в пределах одного окна.
- [x] 3.5 Проверить unit-тестом, что при одновременно заданных
      `--series-interval` и активном новом механизме оба работают:
      `cycle_id` продолжает churnиться по своему расписанию, а
      `churn_generation` — по своему, независимо друг от друга.

## 4. Интеграция и документация

- [x] 4.1 Вручную запустить `avalanche --series-count=1000
      --partial-series-churn-interval=60
      --partial-series-churn-percent=20
      --partial-series-churn-step=10` локально (без
      `--series-interval`, чтобы проверить механизм изолированно),
      снять `/metrics` на каждом шаге и подтвердить через diff значений
      `churn_generation`, что churn применяется небольшими порциями, а
      к концу окна (60с) суммарно churnулось ~20% серий.
- [x] 4.2 Вручную запустить с одновременно заданными
      `--series-interval=30` и новым механизмом (дефолтные
      `--partial-series-churn-interval`/`--partial-series-churn-step`,
      явный `--partial-series-churn-percent=20`), подтвердить, что
      `cycle_id` меняется каждые 30 секунд у всех серий независимо от
      того, что происходит с `churn_generation`.
- [x] 4.3 Обновить `README.md`, описав новые флаги
      (`--partial-series-churn-interval`,
      `--partial-series-churn-percent`, `--partial-series-churn-step`)
      как отдельный от `--series-interval` механизм, с примером,
      использующим дефолты (2ч/30с) плюс явный процент
      (`--partial-series-churn-percent=15`); проверить соответствие
      реальному выводу `--help`.
