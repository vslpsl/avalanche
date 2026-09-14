// Copyright 2022 The Prometheus Authors
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package metricsgen

import (
	"fmt"
	"math"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"gopkg.in/alecthomas/kingpin.v2"
)

type Collector struct {
	cfg    Config
	valGen *rand.Rand

	gauges           []*prometheus.GaugeVec
	counters         []*prometheus.CounterVec
	histograms       []*prometheus.HistogramVec
	nativeHistograms []*prometheus.HistogramVec
	summaries        []*prometheus.SummaryVec
	labelKeys        []string

	updateNotifyCh   chan struct{}
	stopCh           chan struct{}
	valueTick        *time.Ticker
	seriesTick       *time.Ticker
	metricTick       *time.Ticker
	changeSeriesTick *time.Ticker
	churnTick        *time.Ticker

	mu sync.Mutex
}

// NewCollector returns Prometheus collector that can be registered in registry
// that handles metric creation and changes, based on the given configuration.
func NewCollector(cfg Config) *Collector {
	c := &Collector{
		cfg:            cfg,
		valGen:         rand.New(rand.NewSource(time.Now().UnixNano())),
		updateNotifyCh: make(chan struct{}, 1),
		stopCh:         make(chan struct{}),
	}

	if cfg.ValueInterval > 0 {
		c.valueTick = time.NewTicker(time.Duration(cfg.ValueInterval) * time.Second)
	}
	if cfg.SeriesInterval > 0 {
		c.seriesTick = time.NewTicker(time.Duration(cfg.SeriesInterval) * time.Second)
	}
	if cfg.MetricInterval > 0 {
		c.metricTick = time.NewTicker(time.Duration(cfg.MetricInterval) * time.Second)
	}
	if cfg.SeriesChangeInterval > 0 {
		c.changeSeriesTick = time.NewTicker(time.Duration(cfg.SeriesChangeInterval) * time.Second)
	}
	if cfg.PartialSeriesChurnPercent > 0 {
		c.churnTick = time.NewTicker(time.Duration(cfg.PartialSeriesChurnStep) * time.Second)
	}
	return c
}

func (c *Collector) UpdateNotifyCh() chan struct{} {
	return c.updateNotifyCh
}

type Config struct {
	GaugeMetricCount, CounterMetricCount, HistogramMetricCount, NativeHistogramMetricCount, SummaryMetricCount int

	HistogramBuckets, SummaryObjectives int

	LabelCount, SeriesCount        int
	MaxSeriesCount, MinSeriesCount int
	MetricLength, LabelLength      int

	ValueInterval, SeriesInterval, MetricInterval, SeriesChangeInterval, SeriesChangeRate int

	PartialSeriesChurnInterval, PartialSeriesChurnPercent, PartialSeriesChurnStep int

	SpikeMultiplier     float64
	SeriesOperationMode opMode
	ConstLabels         []string
}

func NewConfigFromFlags(flagReg func(name, help string) *kingpin.FlagClause) *Config {
	cfg := &Config{}
	// NOTE: By default avalanche creates 500 gauges, to keep old behaviour. We could break compatibility,
	// but it's less surprising to ask users to adjust and add more types themselves.
	flagReg("gauge-metric-count", "Number of gauge metrics to serve.").Default("500").
		IntVar(&cfg.GaugeMetricCount)
	flagReg("counter-metric-count", "Number of counter metrics to serve.").Default("0").
		IntVar(&cfg.CounterMetricCount)
	flagReg("histogram-metric-count", "Number of explicit (classic) histogram metrics to serve. Use -histogram-metric-bucket-count to control number of buckets. Note that the overall number of series for a single classic histogram metric is equal to 2 (count and sum) + <histogram-metric-bucket-count> + 1 (+Inf bucket).").Default("0").
		IntVar(&cfg.HistogramMetricCount)
	flagReg("histogram-metric-bucket-count", "Number of explicit buckets (classic) histogram metrics, excluding +Inf bucket.").Default("7").
		IntVar(&cfg.HistogramBuckets)
	flagReg("native-histogram-metric-count", "Number of native (exponential) histogram metrics to serve.").Default("0").
		IntVar(&cfg.NativeHistogramMetricCount)
	flagReg("summary-metric-count", "Number of summary metrics to serve.  Use -summary-metric-objective-count to control number of quantile objectives. Note that the overall number of series for a single summary metric is equal to 2 (count and sum) + <summary-metric-objective-count>.").Default("0").
		IntVar(&cfg.SummaryMetricCount)
	flagReg("summary-metric-objective-count", "Number of objectives in the summary metrics to serve.").Default("2").
		IntVar(&cfg.SummaryObjectives)

	flagReg("label-count", "Number of labels per-metric.").Default("10").
		IntVar(&cfg.LabelCount)
	flagReg("series-count", "Number of series per-metric. This excludes the extra series (e.g. _bucket) that will be added for complex types like classic histograms and summaries.").Default("100").
		IntVar(&cfg.SeriesCount)
	flagReg("max-series-count", "Maximum number of series to serve. Applies to 'gradual-change' mode.").Default("1000").
		IntVar(&cfg.MaxSeriesCount)
	flagReg("min-series-count", "Minimum number of series to serve. Applies to 'gradual-change' mode.").Default("100").
		IntVar(&cfg.MinSeriesCount)
	flagReg("spike-multiplier", "Multiplier for the spike mode.").Default("1.5").
		Float64Var(&cfg.SpikeMultiplier)
	flagReg("metricname-length", "Modify length of metric names.").Default("5").
		IntVar(&cfg.MetricLength)
	flagReg("labelname-length", "Modify length of label names.").Default("5").
		IntVar(&cfg.LabelLength)
	flagReg("const-label", "Constant label to add to every metric. Format is labelName=labelValue. Flag can be specified multiple times.").
		StringsVar(&cfg.ConstLabels)

	flagReg("value-interval", "Change series values every {interval} seconds. 0 means no change.").Default("30").
		IntVar(&cfg.ValueInterval)
	flagReg("series-interval", "Change series_id label values every {interval} seconds. 0 means no change.").Default("60").
		IntVar(&cfg.SeriesInterval)
	flagReg("metric-interval", "Change __name__ label values every {interval} seconds. 0 means no change.").Default("0").
		IntVar(&cfg.MetricInterval)
	flagReg("series-change-interval", "Change the number of series every {interval} seconds. Applies to 'gradual-change', 'double-halve' and 'spike' modes. 0 means no change.").Default("30").
		IntVar(&cfg.SeriesChangeInterval)
	flagReg("series-change-rate", "The rate at which the number of active series changes over time. Applies to 'gradual-change' mode.").Default("100").
		IntVar(&cfg.SeriesChangeRate)

	flagReg("partial-series-churn-interval", "Window (seconds) over which --partial-series-churn-percent of series get a new churn_generation label value. Fully independent of --series-interval.").Default("7200").
		IntVar(&cfg.PartialSeriesChurnInterval)
	flagReg("partial-series-churn-percent", "Target percent (0-100) of series that get a new churn_generation label value per window. 0 disables this mechanism entirely (default).").Default("0").
		IntVar(&cfg.PartialSeriesChurnPercent)
	flagReg("partial-series-churn-step", "Step (seconds) at which partial series churn is applied within the window.").Default("30").
		IntVar(&cfg.PartialSeriesChurnStep)

	flagReg("series-operation-mode", "Mode of operation, so optional advanced behaviours on top of --value-interval, --series-interval and --metric-interval.").Default(disabledOpMode).
		EnumVar(&cfg.SeriesOperationMode, disabledOpMode, gradualChangeOpMode, doubleHalveOpMode, spikeOpMode)
	return cfg
}

type opMode = string

const (
	disabledOpMode      opMode = "disabled"
	gradualChangeOpMode opMode = "gradual-change"
	doubleHalveOpMode   opMode = "double-halve"
	spikeOpMode         opMode = "spike"
)

func (c Config) Validate() error {
	if c.MaxSeriesCount <= c.MinSeriesCount {
		return fmt.Errorf("--max-series-count (%d) must be greater than --min-series-count (%d)", c.MaxSeriesCount, c.MinSeriesCount)
	}
	if c.MinSeriesCount < 0 {
		return fmt.Errorf("--min-series-count must be 0 or higher, got %d", c.MinSeriesCount)
	}
	for _, cLabel := range c.ConstLabels {
		split := strings.Split(cLabel, "=")
		if len(split) != 2 {
			return fmt.Errorf("constant label argument must have format labelName=labelValue but got %s", cLabel)
		}
	}

	switch c.SeriesOperationMode {
	case gradualChangeOpMode:
		if c.SeriesChangeRate <= 0 {
			return fmt.Errorf("--series-change-rate must be greater than 0, got %d", c.SeriesChangeRate)
		}
	case spikeOpMode:
		if c.SpikeMultiplier < 1 {
			return fmt.Errorf("--spike-multiplier must be greater than or equal to 1, got %f", c.SpikeMultiplier)
		}
	case doubleHalveOpMode, disabledOpMode:
	default:
		return fmt.Errorf("unknown --series-operation-mode %v", c.SeriesOperationMode)
	}

	if c.PartialSeriesChurnPercent < 0 || c.PartialSeriesChurnPercent > 100 {
		return fmt.Errorf("--partial-series-churn-percent must be between 0 and 100, got %d", c.PartialSeriesChurnPercent)
	}
	if c.PartialSeriesChurnInterval <= 0 {
		return fmt.Errorf("--partial-series-churn-interval must be greater than 0, got %d", c.PartialSeriesChurnInterval)
	}
	if c.PartialSeriesChurnStep <= 0 {
		return fmt.Errorf("--partial-series-churn-step must be greater than 0, got %d", c.PartialSeriesChurnStep)
	}
	if c.PartialSeriesChurnInterval < c.PartialSeriesChurnStep || c.PartialSeriesChurnInterval%c.PartialSeriesChurnStep != 0 {
		return fmt.Errorf("--partial-series-churn-interval (%d) must be greater than or equal to, and evenly divisible by, --partial-series-churn-step (%d)", c.PartialSeriesChurnInterval, c.PartialSeriesChurnStep)
	}
	if c.PartialSeriesChurnPercent > 0 && c.SeriesOperationMode != disabledOpMode {
		return fmt.Errorf("--partial-series-churn-percent is not supported together with --series-operation-mode=%s (only %q is supported), as the runtime-varying series count could exceed the size allocated for churn_generation tracking", c.SeriesOperationMode, disabledOpMode)
	}
	return nil
}

// Describe is used when registering metrics. It's noop avoiding us to have an easier dynamicity.
// No descriptors allow this collector to be "unchecked", but more efficient with what we try to do here.
func (c *Collector) Describe(chan<- *prometheus.Desc) {}

func (c *Collector) Collect(metricCh chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, m := range c.gauges {
		m.Collect(metricCh)
	}
	for _, m := range c.counters {
		m.Collect(metricCh)
	}
	for _, m := range c.histograms {
		m.Collect(metricCh)
	}
	for _, m := range c.nativeHistograms {
		m.Collect(metricCh)
	}
	for _, m := range c.summaries {
		m.Collect(metricCh)
	}
}

func help(mName string) string {
	return fmt.Sprintf("Metric %v is generated by https://github.com/prometheus-community/avalanche project allowing you to load test your Prometheus or Prometheus-compatible systems. It's not too long, not too short to simulate, often chunky descriptions on user metrics. It also contains metric name, so help is slighly different across metrics.", mName)
}

// seriesLabelNames returns the fixed (non-user-configured) variable label
// names every generated series carries, before appending c.labelKeys.
// churn_generation is only declared when the partial series churn mechanism
// is active, to keep the label set unchanged when it's disabled.
func (c *Collector) seriesLabelNames() []string {
	names := []string{"series_id", "cycle_id"}
	if c.cfg.PartialSeriesChurnPercent > 0 {
		names = append(names, "churn_generation")
	}
	return names
}

func (c *Collector) recreateMetrics(unsafeGetState readOnlyStateFn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := unsafeGetState()
	for id := range c.gauges {
		mName := fmt.Sprintf("avalanche_gauge_metric_%s_%v_%v", strings.Repeat("m", c.cfg.MetricLength), s.metricCycle, id)
		gauge := prometheus.NewGaugeVec(
			prometheus.GaugeOpts{Name: mName, Help: help(mName)},
			append(c.seriesLabelNames(), c.labelKeys...),
		)
		c.gauges[id] = gauge
	}
	for id := range c.counters {
		mName := fmt.Sprintf("avalanche_counter_metric_%s_%v_%v_total", strings.Repeat("m", c.cfg.MetricLength), s.metricCycle, id)
		counter := prometheus.NewCounterVec(
			prometheus.CounterOpts{Name: mName, Help: help(mName)},
			append(c.seriesLabelNames(), c.labelKeys...),
		)
		c.counters[id] = counter
	}

	bkts := make([]float64, c.cfg.HistogramBuckets)
	for i := range bkts {
		bkts[i] = 0.0001 * math.Pow10(i)
	}
	for id := range c.histograms {
		mName := fmt.Sprintf("avalanche_histogram_metric_%s_%v_%v", strings.Repeat("m", c.cfg.MetricLength), s.metricCycle, id)
		histogram := prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: mName, Help: help(mName), Buckets: bkts},
			append(c.seriesLabelNames(), c.labelKeys...),
		)
		c.histograms[id] = histogram
	}

	for id := range c.nativeHistograms {
		mName := fmt.Sprintf("avalanche_native_histogram_metric_%s_%v_%v", strings.Repeat("m", c.cfg.MetricLength), s.metricCycle, id)
		histogram := prometheus.NewHistogramVec(
			prometheus.HistogramOpts{Name: mName, Help: help(mName), NativeHistogramBucketFactor: 1.1},
			append(c.seriesLabelNames(), c.labelKeys...),
		)
		c.nativeHistograms[id] = histogram
	}

	// Mimic some quantile objectives.
	objectives := map[float64]float64{}
	if c.cfg.SummaryObjectives > 0 {
		parts := 100 / c.cfg.SummaryObjectives
		for i := 0; i < c.cfg.SummaryObjectives; i++ {
			q := parts * (i + 1)
			if q == 100 {
				q = 99
			}
			objectives[float64(q)/100.0] = float64(100-q) / 1000.0
		}
	}
	for id := range c.summaries {
		mName := fmt.Sprintf("avalanche_summary_metric_%s_%v_%v", strings.Repeat("m", c.cfg.MetricLength), s.metricCycle, id)
		summary := prometheus.NewSummaryVec(
			prometheus.SummaryOpts{Name: mName, Help: help(mName), Objectives: objectives},
			append(c.seriesLabelNames(), c.labelKeys...),
		)
		c.summaries[id] = summary
	}
}

// seriesLabels builds the label set for one series. churnGeneration is the
// full per-series churn_generation slice (nil when the partial series churn
// mechanism is disabled) — the label is only added when it's non-nil, so the
// label set is unchanged when --partial-series-churn-percent=0.
func seriesLabels(seriesID, cycleID int, labelKeys, labelValues []string, churnGeneration []int32) prometheus.Labels {
	labels := prometheus.Labels{
		"series_id": fmt.Sprintf("%v", seriesID),
		"cycle_id":  fmt.Sprintf("%v", cycleID),
	}
	if churnGeneration != nil {
		labels["churn_generation"] = fmt.Sprintf("%v", churnGeneration[seriesID])
	}
	for idx, key := range labelKeys {
		labels[key] = labelValues[idx]
	}
	return labels
}

type seriesDeleter interface {
	Delete(labels prometheus.Labels) bool
}

func deleteValues[T seriesDeleter](metrics []T, labelKeys []string, s metricState) {
	for _, metric := range metrics {
		for idx := 0; idx < s.seriesCount; idx++ {
			labels := seriesLabels(idx, s.seriesCycle, labelKeys, s.labelValues, s.churnGeneration)
			metric.Delete(labels)
		}
	}
}

func (c *Collector) cycleValues(unsafeGetState readOnlyStateFn) {
	c.mu.Lock()
	defer c.mu.Unlock()

	s := unsafeGetState()
	for idx := 0; idx < s.seriesCount; idx++ {
		labels := seriesLabels(idx, s.seriesCycle, c.labelKeys, s.labelValues, s.churnGeneration)
		for _, metric := range c.gauges {
			metric.With(labels).Set(float64(c.valGen.Intn(100)))
		}
		for _, metric := range c.counters {
			metric.With(labels).Add(float64(c.valGen.Intn(100)))
		}
		for _, metric := range c.histograms {
			metric.With(labels).Observe(float64(c.valGen.Intn(100)))
		}
		for _, metric := range c.nativeHistograms {
			metric.With(labels).Observe(float64(c.valGen.Intn(100)))
		}
		for _, metric := range c.summaries {
			metric.With(labels).Observe(float64(c.valGen.Intn(100)))
		}
	}
}

func (c *Collector) handleValueTicks(unsafeGetState readOnlyStateFn) {
	if c.valueTick == nil {
		return
	}
	for tick := range c.valueTick.C {
		fmt.Printf("%v: refreshing metric values\n", tick)
		c.cycleValues(unsafeGetState)

		select {
		case c.updateNotifyCh <- struct{}{}:
		default:
		}
	}
}

func (c *Collector) handleSeriesTicks(seriesCycle *int, unsafeGetState readOnlyStateFn) {
	if c.seriesTick == nil {
		return
	}
	for tick := range c.seriesTick.C {
		c.mu.Lock()
		fmt.Printf("%v: refreshing series cycle\n", tick)
		s := unsafeGetState()
		deleteValues(c.gauges, c.labelKeys, s)
		deleteValues(c.counters, c.labelKeys, s)
		deleteValues(c.histograms, c.labelKeys, s)
		deleteValues(c.nativeHistograms, c.labelKeys, s)
		deleteValues(c.summaries, c.labelKeys, s)
		*seriesCycle++
		c.mu.Unlock()
		c.cycleValues(unsafeGetState)

		select {
		case c.updateNotifyCh <- struct{}{}:
		default:
		}
	}
}

func (c *Collector) handleMetricTicks(metricCycle *int, unsafeGetState readOnlyStateFn) {
	if c.metricTick == nil {
		return
	}

	for tick := range c.metricTick.C {
		fmt.Printf("%v: refreshing metric cycle\n", tick)
		*metricCycle++
		c.recreateMetrics(unsafeGetState)
		select {
		case c.updateNotifyCh <- struct{}{}:
		default:
		}
	}
}

func changeSeriesGradual(seriesChangeRate, maxSeriesCount, minSeriesCount int, currentSeriesCount *int, seriesIncrease *bool) {
	fmt.Printf("Current series count: %d\n", *currentSeriesCount)
	if *seriesIncrease {
		*currentSeriesCount += seriesChangeRate
		if *currentSeriesCount >= maxSeriesCount {
			*currentSeriesCount = maxSeriesCount
			*seriesIncrease = false
		}
	} else {
		*currentSeriesCount -= seriesChangeRate
		if *currentSeriesCount < minSeriesCount {
			*currentSeriesCount = minSeriesCount
			*seriesIncrease = true
		}
	}
}

func changeSeriesDoubleHalve(currentSeriesCount *int, seriesIncrease *bool) {
	if *seriesIncrease {
		*currentSeriesCount *= 2
	} else {
		*currentSeriesCount /= 2
		if *currentSeriesCount < 1 {
			*currentSeriesCount = 1
		}
	}
	*seriesIncrease = !*seriesIncrease
}

func (c *Collector) handleDoubleHalveMode(seriesCount *int, unsafeGetState readOnlyStateFn) {
	if c.changeSeriesTick == nil {
		return
	}

	seriesIncrease := true
	for tick := range c.changeSeriesTick.C {
		c.recreateMetrics(unsafeGetState)
		c.cycleValues(unsafeGetState)

		c.mu.Lock()
		changeSeriesDoubleHalve(seriesCount, &seriesIncrease)
		fmt.Printf("%v: Adjusting series count. New count: %d\n", tick, *seriesCount)
		c.mu.Unlock()

		select {
		case c.updateNotifyCh <- struct{}{}:
		default:
		}
	}
}

func (c *Collector) handleGradualChangeMode(seriesCount *int, unsafeGetState readOnlyStateFn) {
	if c.changeSeriesTick == nil {
		return
	}

	seriesIncrease := true
	for tick := range c.changeSeriesTick.C {
		c.recreateMetrics(unsafeGetState)
		c.cycleValues(unsafeGetState)

		c.mu.Lock()
		changeSeriesGradual(c.cfg.SeriesChangeRate, c.cfg.MaxSeriesCount, c.cfg.MinSeriesCount, seriesCount, &seriesIncrease)
		fmt.Printf("%v: Adjusting series count. New count: %d\n", tick, *seriesCount)
		c.mu.Unlock()

		select {
		case c.updateNotifyCh <- struct{}{}:
		default:
		}
	}
}

func (c *Collector) handleSpikeMode(seriesCount *int, unsafeGetState readOnlyStateFn, spikeMultiplier float64) {
	if c.changeSeriesTick == nil {
		return
	}

	initialSeriesCount := *seriesCount
	for tick := range c.changeSeriesTick.C {
		c.recreateMetrics(unsafeGetState)
		c.cycleValues(unsafeGetState)

		c.mu.Lock()
		if *seriesCount > initialSeriesCount {
			*seriesCount = initialSeriesCount
		} else {
			*seriesCount = int(float64(initialSeriesCount) * spikeMultiplier)
		}
		fmt.Printf("%v: Adjusting series count. New count: %d\n", tick, *seriesCount)
		c.mu.Unlock()

		select {
		case c.updateNotifyCh <- struct{}{}:
		default:
		}
	}
}

// metricState represents current state of ids, cycles and current series.
type metricState struct {
	seriesCount, seriesCycle, metricCycle int

	labelValues []string

	// churnGeneration holds the per-series-slot generation counter used by
	// the partial series churn mechanism (churn_generation label). It is
	// nil when --partial-series-churn-percent=0 (mechanism disabled).
	churnGeneration []int32
}

// shuffledPool returns a Fisher-Yates permutation of [0, n) using r.
func shuffledPool(n int, r *rand.Rand) []int32 {
	pool := make([]int32, n)
	for i := range pool {
		pool[i] = int32(i)
	}
	for i := n - 1; i > 0; i-- {
		j := r.Intn(i + 1)
		pool[i], pool[j] = pool[j], pool[i]
	}
	return pool
}

// cumulativeChurnTarget returns the cumulative number of series that should
// have been churned by the given step (1-indexed) out of steps total steps
// in the window, for a window target of windowTargetCount series.
func cumulativeChurnTarget(windowTargetCount, step, steps int) int {
	return int(math.Round(float64(windowTargetCount) * float64(step) / float64(steps)))
}

// churnSeriesSlot bumps the churn_generation of a single series slot (idx)
// and pushes that change into every exposed metric vector: a Prometheus vec
// identifies a series by its full label set, so simply mutating
// churnGeneration in memory is not observable on its own — the old
// (pre-increment) label combination must be deleted and the new one created,
// mirroring what deleteValues+cycleValues do for --series-interval, but
// scoped to just this one slot. Must be called with c.mu held (uses
// c.valGen).
func (c *Collector) churnSeriesSlot(idx int, s metricState) {
	oldLabels := seriesLabels(idx, s.seriesCycle, c.labelKeys, s.labelValues, s.churnGeneration)
	for _, m := range c.gauges {
		m.Delete(oldLabels)
	}
	for _, m := range c.counters {
		m.Delete(oldLabels)
	}
	for _, m := range c.histograms {
		m.Delete(oldLabels)
	}
	for _, m := range c.nativeHistograms {
		m.Delete(oldLabels)
	}
	for _, m := range c.summaries {
		m.Delete(oldLabels)
	}

	s.churnGeneration[idx]++

	newLabels := seriesLabels(idx, s.seriesCycle, c.labelKeys, s.labelValues, s.churnGeneration)
	for _, m := range c.gauges {
		m.With(newLabels).Set(float64(c.valGen.Intn(100)))
	}
	for _, m := range c.counters {
		m.With(newLabels).Add(float64(c.valGen.Intn(100)))
	}
	for _, m := range c.histograms {
		m.With(newLabels).Observe(float64(c.valGen.Intn(100)))
	}
	for _, m := range c.nativeHistograms {
		m.With(newLabels).Observe(float64(c.valGen.Intn(100)))
	}
	for _, m := range c.summaries {
		m.With(newLabels).Observe(float64(c.valGen.Intn(100)))
	}
}

// handleChurnTicks drives the partial series churn mechanism: every
// --partial-series-churn-step seconds, it churns (bumps churn_generation of,
// and re-exposes) a growing, non-overlapping slice of a persistent shuffled
// series-slot pool, until --partial-series-churn-percent of all series have
// been churned by the end of the --partial-series-churn-interval window; the
// pool is then reshuffled and the window restarts. This is fully independent
// of --series-interval/handleSeriesTicks and cycle_id: it reads the current
// cycle_id (via unsafeGetState) so churned series stay consistent with
// whatever --series-interval last set, but never changes it.
func (c *Collector) handleChurnTicks(unsafeGetState readOnlyStateFn) {
	if c.churnTick == nil {
		return
	}

	seriesCount := c.cfg.SeriesCount
	steps := c.cfg.PartialSeriesChurnInterval / c.cfg.PartialSeriesChurnStep
	windowTargetCount := seriesCount * c.cfg.PartialSeriesChurnPercent / 100

	c.mu.Lock()
	pool := shuffledPool(seriesCount, c.valGen)
	c.mu.Unlock()

	churnedSoFar, stepInWindow := 0, 0

	for tick := range c.churnTick.C {
		stepInWindow++
		target := cumulativeChurnTarget(windowTargetCount, stepInWindow, steps)

		c.mu.Lock()
		s := unsafeGetState()
		for _, idx32 := range pool[churnedSoFar:target] {
			c.churnSeriesSlot(int(idx32), s)
		}
		churnedSoFar = target
		if stepInWindow == steps {
			fmt.Printf("%v: partial series churn window complete (%d/%d series churned), reshuffling\n", tick, churnedSoFar, seriesCount)
			pool = shuffledPool(seriesCount, c.valGen)
			churnedSoFar, stepInWindow = 0, 0
		}
		c.mu.Unlock()

		select {
		case c.updateNotifyCh <- struct{}{}:
		default:
		}
	}
}

type readOnlyStateFn func() metricState

// Run creates a set of Prometheus test series that update over time.
// NOTE: Only one execution of RunMetrics is currently expected.
func (c *Collector) Run() error {
	labelKeys := make([]string, c.cfg.LabelCount)
	for idx := 0; idx < c.cfg.LabelCount; idx++ {
		labelKeys[idx] = fmt.Sprintf("label_key_%s_%v", strings.Repeat("k", c.cfg.LabelLength), idx)
	}
	labelValues := make([]string, c.cfg.LabelCount)
	for idx := 0; idx < c.cfg.LabelCount; idx++ {
		labelValues[idx] = fmt.Sprintf("label_val_%s_%v", strings.Repeat("v", c.cfg.LabelLength), idx)
	}
	for _, cLabel := range c.cfg.ConstLabels {
		split := strings.Split(cLabel, "=")
		labelKeys = append(labelKeys, split[0])
		labelValues = append(labelValues, split[1])
	}

	var churnGeneration []int32
	if c.cfg.PartialSeriesChurnPercent > 0 {
		churnGeneration = make([]int32, c.cfg.SeriesCount)
	}
	mutableState := &metricState{seriesCount: c.cfg.SeriesCount, labelValues: labelValues, churnGeneration: churnGeneration}
	// unsafe means you need to lock c.mu to use it.
	unsafeReadOnlyGetState := func() metricState { return *mutableState }

	c.mu.Lock() // Just to make race detector happy, not really needed in practice.
	c.labelKeys = labelKeys
	c.gauges = make([]*prometheus.GaugeVec, c.cfg.GaugeMetricCount)
	c.counters = make([]*prometheus.CounterVec, c.cfg.CounterMetricCount)
	c.histograms = make([]*prometheus.HistogramVec, c.cfg.HistogramMetricCount)
	c.nativeHistograms = make([]*prometheus.HistogramVec, c.cfg.NativeHistogramMetricCount)
	c.summaries = make([]*prometheus.SummaryVec, c.cfg.SummaryMetricCount)
	c.mu.Unlock()

	c.recreateMetrics(unsafeReadOnlyGetState)

	switch c.cfg.SeriesOperationMode {
	case doubleHalveOpMode:
		fmt.Printf("Starting double-halve mode; starting series: %d, change series interval: %d seconds\n", c.cfg.SeriesCount, c.cfg.SeriesChangeInterval)
		go c.handleDoubleHalveMode(&mutableState.seriesCount, unsafeReadOnlyGetState)

	case gradualChangeOpMode:
		fmt.Printf("Starting gradual-change mode; min series: %d, max series: %d, series change rate: %d, change series interval: %d seconds\n", c.cfg.MinSeriesCount, c.cfg.MaxSeriesCount, c.cfg.SeriesChangeRate, c.cfg.SeriesChangeInterval)
		c.mu.Lock()
		mutableState.seriesCount = c.cfg.MinSeriesCount
		c.mu.Unlock()
		go c.handleGradualChangeMode(&mutableState.seriesCount, unsafeReadOnlyGetState)

	case spikeOpMode:
		fmt.Printf("Starting spike mode; initial series: %d, spike multiplier: %f, spike interval: %v\n", c.cfg.SeriesCount, c.cfg.SpikeMultiplier, c.cfg.SeriesChangeInterval)
		go c.handleSpikeMode(&mutableState.seriesCount, unsafeReadOnlyGetState, c.cfg.SpikeMultiplier)
	}
	c.cycleValues(unsafeReadOnlyGetState)

	go c.handleValueTicks(unsafeReadOnlyGetState)
	go c.handleSeriesTicks(&mutableState.seriesCycle, unsafeReadOnlyGetState)
	go c.handleMetricTicks(&mutableState.metricCycle, unsafeReadOnlyGetState)
	go c.handleChurnTicks(unsafeReadOnlyGetState)

	// Mark best-effort update, so remote write knows (if enabled).
	select {
	case c.updateNotifyCh <- struct{}{}:
	default:
	}

	<-c.stopCh
	return nil
}

func (c *Collector) Stop(_ error) {
	if c.valueTick != nil {
		c.valueTick.Stop()
	}
	if c.seriesTick != nil {
		c.seriesTick.Stop()
	}
	if c.metricTick != nil {
		c.metricTick.Stop()
	}
	if c.changeSeriesTick != nil {
		c.changeSeriesTick.Stop()
	}
	if c.churnTick != nil {
		c.churnTick.Stop()
	}
	close(c.stopCh)
}
