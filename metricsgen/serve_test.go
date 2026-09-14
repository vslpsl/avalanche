// Copyright 2024 The Prometheus Authors
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
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	io_prometheus_client "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Helper function to count the series in the registry
func countSeries(t *testing.T, registry *prometheus.Registry) (seriesCount int) {
	t.Helper()

	metricsFamilies, err := registry.Gather()
	assert.NoError(t, err)

	for _, mf := range metricsFamilies {
		for range mf.Metric {
			seriesCount++
		}
	}
	return seriesCount
}

// countSeriesTypes gives exact count of all types. For complex types that are represented by counters in Prometheus
// data model (and text exposition formats), we count all individual resulting series.
func countSeriesTypes(t *testing.T, registry *prometheus.Registry) (gauges, counters, histograms, nhistograms, summaries int) {
	t.Helper()

	metricsFamilies, err := registry.Gather()
	assert.NoError(t, err)

	for _, mf := range metricsFamilies {
		for _, m := range mf.Metric {
			switch mf.GetType() {
			case io_prometheus_client.MetricType_GAUGE:
				gauges++
			case io_prometheus_client.MetricType_COUNTER:
				counters++
			case io_prometheus_client.MetricType_HISTOGRAM:
				if bkts := len(m.GetHistogram().Bucket); bkts == 0 {
					nhistograms++
				} else {
					histograms += 2 // count and sum.
					histograms += len(m.GetHistogram().GetBucket())
					if m.GetHistogram().GetBucket()[bkts-1].GetUpperBound() != math.Inf(+1) {
						// In the proto model we don't put explicit +Inf bucket, unless there is an exemplar,
						// but it will appear as series in text format and Prometheus model. Account for that.
						histograms++
					}
				}
			case io_prometheus_client.MetricType_SUMMARY:
				summaries += 2 // count and sum.
				summaries += len(m.GetSummary().GetQuantile())
			default:
				t.Fatalf("unknown metric type found %v", mf.GetType())
			}
		}
	}
	return gauges, counters, histograms, nhistograms, summaries
}

func TestRunMetrics(t *testing.T) {
	testCfg := Config{
		GaugeMetricCount:           200,
		CounterMetricCount:         200,
		HistogramMetricCount:       10,
		HistogramBuckets:           7,
		NativeHistogramMetricCount: 10,
		SummaryMetricCount:         10,
		SummaryObjectives:          2,
		SeriesOperationMode:        disabledOpMode,

		MinSeriesCount: 0,
		MaxSeriesCount: 1000,
		LabelCount:     1,
		SeriesCount:    10,
		MetricLength:   1,
		LabelLength:    1,
		ConstLabels:    []string{"constLabel=test"},

		PartialSeriesChurnInterval: 7200,
		PartialSeriesChurnStep:     30,
		RuleGroupSize:              10,
		RuleEvalInterval:           60,
	}
	assert.NoError(t, testCfg.Validate())

	reg := prometheus.NewRegistry()
	coll := NewCollector(testCfg)
	reg.MustRegister(coll)

	go coll.Run()
	t.Cleanup(func() {
		coll.Stop(nil)
	})

	time.Sleep(2 * time.Second)

	g, c, h, nh, s := countSeriesTypes(t, reg)
	assert.Equal(t, testCfg.GaugeMetricCount*testCfg.SeriesCount, g)
	assert.Equal(t, testCfg.CounterMetricCount*testCfg.SeriesCount, c)
	assert.Equal(t, (2+testCfg.HistogramBuckets+1)*testCfg.HistogramMetricCount*testCfg.SeriesCount, h)
	assert.Equal(t, testCfg.NativeHistogramMetricCount*testCfg.SeriesCount, nh)
	assert.Equal(t, (2+testCfg.SummaryObjectives)*testCfg.SummaryMetricCount*testCfg.SeriesCount, s)
}

func TestRunMetrics_ValueChange_SeriesCountSame(t *testing.T) {
	testCfg := Config{
		GaugeMetricCount:           200,
		CounterMetricCount:         200,
		HistogramMetricCount:       10,
		HistogramBuckets:           7,
		NativeHistogramMetricCount: 10,
		SummaryMetricCount:         10,
		SummaryObjectives:          2,
		SeriesOperationMode:        disabledOpMode,

		MinSeriesCount: 0,
		MaxSeriesCount: 1000,
		LabelCount:     1,
		SeriesCount:    10,
		MetricLength:   1,
		LabelLength:    1,
		ConstLabels:    []string{"constLabel=test"},

		ValueInterval: 1, // Change value every second.

		PartialSeriesChurnInterval: 7200,
		PartialSeriesChurnStep:     30,
		RuleGroupSize:              10,
		RuleEvalInterval:           60,
	}
	assert.NoError(t, testCfg.Validate())

	reg := prometheus.NewRegistry()
	coll := NewCollector(testCfg)
	reg.MustRegister(coll)

	go coll.Run()
	t.Cleanup(func() {
		coll.Stop(nil)
	})

	// We can't assert value, or even it's change without mocking random generator,
	// but let's at least assert series count does not change.
	for i := 0; i < 5; i++ {
		time.Sleep(2 * time.Second)

		g, c, h, nh, s := countSeriesTypes(t, reg)
		assert.Equal(t, testCfg.GaugeMetricCount*testCfg.SeriesCount, g)
		assert.Equal(t, testCfg.CounterMetricCount*testCfg.SeriesCount, c)
		assert.Equal(t, (2+testCfg.HistogramBuckets+1)*testCfg.HistogramMetricCount*testCfg.SeriesCount, h)
		assert.Equal(t, testCfg.NativeHistogramMetricCount*testCfg.SeriesCount, nh)
		assert.Equal(t, (2+testCfg.SummaryObjectives)*testCfg.SummaryMetricCount*testCfg.SeriesCount, s)
	}
}

func currentCycleID(t *testing.T, registry *prometheus.Registry) (cycleID int) {
	t.Helper()

	metricsFamilies, err := registry.Gather()
	assert.NoError(t, err)

	cycleID = -1
	for _, mf := range metricsFamilies {
		for _, m := range mf.Metric {
			for _, l := range m.GetLabel() {
				if l.GetName() == "cycle_id" {
					gotCycleID, err := strconv.Atoi(l.GetValue())
					require.NoError(t, err)

					if cycleID == -1 {
						cycleID = gotCycleID
						continue
					}
					if cycleID != gotCycleID {
						t.Fatalf("expected cycle ID to be the same across all metrics, previous metric had cycle_id=%v; now found %v", cycleID, m.GetLabel())
					}
				}
			}
		}
	}
	return cycleID
}

func TestRunMetrics_SeriesChurn(t *testing.T) {
	testCfg := Config{
		GaugeMetricCount:           200,
		CounterMetricCount:         200,
		HistogramMetricCount:       10,
		HistogramBuckets:           7,
		NativeHistogramMetricCount: 10,
		SummaryMetricCount:         10,
		SummaryObjectives:          2,
		SeriesOperationMode:        disabledOpMode,

		MinSeriesCount: 0,
		MaxSeriesCount: 1000,
		LabelCount:     1,
		SeriesCount:    10,
		MetricLength:   1,
		LabelLength:    1,
		ConstLabels:    []string{"constLabel=test"},

		SeriesInterval: 1, // Churn series every second.
		// Change value every second too, there was a regression when both value and series cycle.
		ValueInterval: 1,

		PartialSeriesChurnInterval: 7200,
		PartialSeriesChurnStep:     30,
		RuleGroupSize:              10,
		RuleEvalInterval:           60,
	}
	assert.NoError(t, testCfg.Validate())

	reg := prometheus.NewRegistry()
	coll := NewCollector(testCfg)
	reg.MustRegister(coll)

	go coll.Run()
	t.Cleanup(func() {
		coll.Stop(nil)
	})

	cycleID := -1
	// No matter how much time we wait, we should see always same series count, just
	// different cycle_id.
	for i := 0; i < 5; i++ {
		time.Sleep(2 * time.Second)

		g, c, h, nh, s := countSeriesTypes(t, reg)
		assert.Equal(t, testCfg.GaugeMetricCount*testCfg.SeriesCount, g)
		assert.Equal(t, testCfg.CounterMetricCount*testCfg.SeriesCount, c)
		assert.Equal(t, (2+testCfg.HistogramBuckets+1)*testCfg.HistogramMetricCount*testCfg.SeriesCount, h)
		assert.Equal(t, testCfg.NativeHistogramMetricCount*testCfg.SeriesCount, nh)
		assert.Equal(t, (2+testCfg.SummaryObjectives)*testCfg.SummaryMetricCount*testCfg.SeriesCount, s)

		gotCycleID := currentCycleID(t, reg)
		require.Greater(t, gotCycleID, cycleID)
		cycleID = gotCycleID
	}
}

func TestRunMetricsSeriesCountChangeDoubleHalve(t *testing.T) {
	testCfg := Config{
		GaugeMetricCount:     1,
		LabelCount:           1,
		SeriesCount:          5, // Initial.
		MaxSeriesCount:       10,
		MinSeriesCount:       1,
		SpikeMultiplier:      1.5,
		SeriesChangeRate:     1,
		MetricLength:         1,
		LabelLength:          1,
		ValueInterval:        100,
		SeriesInterval:       100,
		MetricInterval:       100,
		SeriesChangeInterval: 3,
		SeriesOperationMode:  doubleHalveOpMode,
		ConstLabels:          []string{"constLabel=test"},

		PartialSeriesChurnInterval: 7200,
		PartialSeriesChurnStep:     30,
		RuleGroupSize:              10,
		RuleEvalInterval:           60,
	}
	assert.NoError(t, testCfg.Validate())

	reg := prometheus.NewRegistry()
	coll := NewCollector(testCfg)
	reg.MustRegister(coll)

	go coll.Run()
	t.Cleanup(func() {
		coll.Stop(nil)
	})

	time.Sleep(2 * time.Second)
	for i := 0; i < 4; i++ {
		time.Sleep(time.Duration(testCfg.SeriesChangeInterval) * time.Second)
		if i%2 == 0 { // Expecting halved series count
			currentCount := countSeries(t, reg)
			expectedCount := testCfg.SeriesCount
			assert.Equal(t, expectedCount, currentCount, "Halved series count should be %d but got %d", expectedCount, currentCount)
		} else { // Expecting doubled series count
			currentCount := countSeries(t, reg)
			expectedCount := testCfg.SeriesCount * 2
			assert.Equal(t, expectedCount, currentCount, "Doubled series count should be %d but got %d", expectedCount, currentCount)
		}
	}
}

func TestRunMetricsGradualChange(t *testing.T) {
	testCfg := Config{
		GaugeMetricCount:     1,
		LabelCount:           1,
		SeriesCount:          100, // Initial.
		MaxSeriesCount:       30,
		MinSeriesCount:       10,
		SpikeMultiplier:      1.5,
		SeriesChangeRate:     10,
		MetricLength:         1,
		LabelLength:          1,
		ValueInterval:        100,
		SeriesInterval:       100,
		MetricInterval:       100,
		SeriesChangeInterval: 3,
		SeriesOperationMode:  gradualChangeOpMode,
		ConstLabels:          []string{"constLabel=test"},

		PartialSeriesChurnInterval: 7200,
		PartialSeriesChurnStep:     30,
		RuleGroupSize:              10,
		RuleEvalInterval:           60,
	}
	assert.NoError(t, testCfg.Validate())

	reg := prometheus.NewRegistry()
	coll := NewCollector(testCfg)
	reg.MustRegister(coll)

	go coll.Run()
	t.Cleanup(func() {
		coll.Stop(nil)
	})

	time.Sleep(2 * time.Second)
	currentCount := countSeries(t, reg)
	fmt.Println("seriesCount: ", currentCount)
	assert.Equal(t, testCfg.MinSeriesCount, currentCount, "Initial series count should be minSeriesCount %d but got %d", testCfg.MinSeriesCount, currentCount)

	assert.Eventually(t, func() bool {
		graduallyIncreasedCount := countSeries(t, reg)
		fmt.Println("seriesCount: ", graduallyIncreasedCount)
		if graduallyIncreasedCount > testCfg.MaxSeriesCount {
			t.Fatalf("Gradually increased series count should be less than maxSeriesCount %d but got %d", testCfg.MaxSeriesCount, graduallyIncreasedCount)
		}
		if currentCount > graduallyIncreasedCount {
			t.Fatalf("Gradually increased series count should be greater than initial series count %d but got %d", currentCount, graduallyIncreasedCount)
		} else {
			currentCount = graduallyIncreasedCount
		}

		return graduallyIncreasedCount == testCfg.MaxSeriesCount
	}, 15*time.Second, time.Duration(testCfg.SeriesChangeInterval)*time.Second, "Did not receive update notification for series count gradual increase in time")

	assert.Eventually(t, func() bool {
		graduallyIncreasedCount := countSeries(t, reg)
		fmt.Println("seriesCount: ", graduallyIncreasedCount)
		if graduallyIncreasedCount < testCfg.MinSeriesCount {
			t.Fatalf("Gradually increased series count should be less than maxSeriesCount %d but got %d", testCfg.MaxSeriesCount, graduallyIncreasedCount)
		}

		return graduallyIncreasedCount == testCfg.MinSeriesCount
	}, 15*time.Second, time.Duration(testCfg.SeriesChangeInterval)*time.Second, "Did not receive update notification for series count gradual increase in time")
}

func TestRunMetricsWithInvalidSeriesCounts(t *testing.T) {
	testCfg := Config{
		GaugeMetricCount:     1,
		LabelCount:           1,
		SeriesCount:          100,
		MaxSeriesCount:       10,
		MinSeriesCount:       100,
		SpikeMultiplier:      1.5,
		SeriesChangeRate:     10,
		MetricLength:         1,
		LabelLength:          1,
		ValueInterval:        100,
		SeriesInterval:       100,
		MetricInterval:       100,
		SeriesChangeInterval: 3,
		SeriesOperationMode:  gradualChangeOpMode,
		ConstLabels:          []string{"constLabel=test"},
	}
	assert.Error(t, testCfg.Validate())
}

func TestRunMetricsSpikeChange(t *testing.T) {
	testCfg := Config{
		GaugeMetricCount:     1,
		LabelCount:           1,
		SeriesCount:          100,
		MaxSeriesCount:       30,
		MinSeriesCount:       10,
		SpikeMultiplier:      1.5,
		SeriesChangeRate:     10,
		MetricLength:         1,
		LabelLength:          1,
		ValueInterval:        100,
		SeriesInterval:       100,
		MetricInterval:       100,
		SeriesChangeInterval: 10,
		SeriesOperationMode:  spikeOpMode,
		ConstLabels:          []string{"constLabel=test"},

		PartialSeriesChurnInterval: 7200,
		PartialSeriesChurnStep:     30,
		RuleGroupSize:              10,
		RuleEvalInterval:           60,
	}
	assert.NoError(t, testCfg.Validate())

	reg := prometheus.NewRegistry()
	coll := NewCollector(testCfg)
	reg.MustRegister(coll)

	go coll.Run()
	t.Cleanup(func() {
		coll.Stop(nil)
	})

	time.Sleep(2 * time.Second)
	for i := 0; i < 4; i++ {
		time.Sleep(time.Duration(testCfg.SeriesChangeInterval) * time.Second)
		if i%2 == 0 {
			currentCount := countSeries(t, reg)
			expectedCount := testCfg.SeriesCount
			assert.Equal(t, expectedCount, currentCount, fmt.Sprintf("Halved series count should be %d but got %d", expectedCount, currentCount))
		} else {
			currentCount := countSeries(t, reg)
			expectedCount := int(float64(testCfg.SeriesCount) * testCfg.SpikeMultiplier)
			assert.Equal(t, expectedCount, currentCount, fmt.Sprintf("Multiplied the series count by %.1f, should be %d but got %d", testCfg.SpikeMultiplier, expectedCount, currentCount))
		}
	}
}

// churnedCount returns the number of distinct series slots (deduplicated by
// series_id across metric families) whose churn_generation label is present
// and non-zero.
func churnedCount(t *testing.T, registry *prometheus.Registry) int {
	t.Helper()

	metricsFamilies, err := registry.Gather()
	assert.NoError(t, err)

	seen := make(map[string]bool)
	churned := 0
	for _, mf := range metricsFamilies {
		for _, m := range mf.Metric {
			var seriesID, churnGen string
			for _, l := range m.GetLabel() {
				switch l.GetName() {
				case "series_id":
					seriesID = l.GetValue()
				case "churn_generation":
					churnGen = l.GetValue()
				}
			}
			if seriesID == "" || seen[seriesID] {
				continue
			}
			seen[seriesID] = true
			if churnGen != "" && churnGen != "0" {
				churned++
			}
		}
	}
	return churned
}

func TestSeriesLabels_ChurnGenerationAbsentWhenNil(t *testing.T) {
	labels := seriesLabels(0, 0, nil, nil, nil)
	assert.NotContains(t, labels, "churn_generation")
}

func TestSeriesLabels_ChurnGenerationPresentWhenSet(t *testing.T) {
	labels := seriesLabels(1, 0, nil, nil, []int32{0, 5, 0})
	assert.Equal(t, "5", labels["churn_generation"])
}

func TestShuffledPool_IsPermutation(t *testing.T) {
	pool := shuffledPool(1000, rand.New(rand.NewSource(1)))
	require.Len(t, pool, 1000)

	seen := make(map[int32]bool, 1000)
	for _, v := range pool {
		assert.False(t, seen[v], "value %d appeared more than once in the pool", v)
		seen[v] = true
	}
	assert.Len(t, seen, 1000)
}

func TestCumulativeChurnTarget_EvenSteps(t *testing.T) {
	assert.Equal(t, 50, cumulativeChurnTarget(200, 1, 4))
	assert.Equal(t, 100, cumulativeChurnTarget(200, 2, 4))
	assert.Equal(t, 150, cumulativeChurnTarget(200, 3, 4))
	assert.Equal(t, 200, cumulativeChurnTarget(200, 4, 4))
}

func TestCumulativeChurnTarget_RoundingReachesExactTargetAtWindowEnd(t *testing.T) {
	assert.Equal(t, 170, cumulativeChurnTarget(170, 6, 6))
}

func TestPartialSeriesChurnValidation(t *testing.T) {
	base := Config{
		SeriesOperationMode:        disabledOpMode,
		MaxSeriesCount:             10,
		MinSeriesCount:             0,
		PartialSeriesChurnInterval: 7200,
		PartialSeriesChurnPercent:  0,
		PartialSeriesChurnStep:     30,
		RuleGroupSize:              10,
		RuleEvalInterval:           60,
	}
	assert.NoError(t, base.Validate(), "defaults must be valid on their own")

	percentTooHigh := base
	percentTooHigh.PartialSeriesChurnPercent = 150
	assert.Error(t, percentTooHigh.Validate())

	percentNegative := base
	percentNegative.PartialSeriesChurnPercent = -1
	assert.Error(t, percentNegative.Validate())

	zeroInterval := base
	zeroInterval.PartialSeriesChurnInterval = 0
	assert.Error(t, zeroInterval.Validate())

	zeroStep := base
	zeroStep.PartialSeriesChurnStep = 0
	assert.Error(t, zeroStep.Validate())

	notDivisible := base
	notDivisible.PartialSeriesChurnInterval = 1000
	notDivisible.PartialSeriesChurnStep = 300
	assert.Error(t, notDivisible.Validate())

	stepBiggerThanInterval := base
	stepBiggerThanInterval.PartialSeriesChurnInterval = 30
	stepBiggerThanInterval.PartialSeriesChurnStep = 60
	assert.Error(t, stepBiggerThanInterval.Validate())

	withOpMode := base
	withOpMode.PartialSeriesChurnPercent = 20
	withOpMode.SeriesOperationMode = gradualChangeOpMode
	withOpMode.SeriesChangeRate = 1
	assert.Error(t, withOpMode.Validate(), "partial series churn must reject --series-operation-mode combos")
}

func TestRunMetrics_PartialSeriesChurn_DisabledByDefault(t *testing.T) {
	testCfg := Config{
		GaugeMetricCount:    50,
		LabelCount:          1,
		SeriesCount:         20,
		MetricLength:        1,
		LabelLength:         1,
		MaxSeriesCount:      100,
		MinSeriesCount:      0,
		SeriesOperationMode: disabledOpMode,

		// PartialSeriesChurnPercent left at its zero value (0) = disabled.
		PartialSeriesChurnInterval: 7200,
		PartialSeriesChurnStep:     30,
		RuleGroupSize:              10,
		RuleEvalInterval:           60,
	}
	assert.NoError(t, testCfg.Validate())

	reg := prometheus.NewRegistry()
	coll := NewCollector(testCfg)
	reg.MustRegister(coll)

	go coll.Run()
	t.Cleanup(func() {
		coll.Stop(nil)
	})

	time.Sleep(1 * time.Second)

	metricsFamilies, err := reg.Gather()
	require.NoError(t, err)
	require.NotEmpty(t, metricsFamilies)
	for _, mf := range metricsFamilies {
		for _, m := range mf.Metric {
			for _, l := range m.GetLabel() {
				assert.NotEqual(t, "churn_generation", l.GetName(), "churn_generation must not appear when --partial-series-churn-percent=0")
			}
		}
	}
}

func TestRunMetrics_PartialSeriesChurn_CumulativeTargets(t *testing.T) {
	testCfg := Config{
		GaugeMetricCount:    1,
		LabelCount:          1,
		SeriesCount:         100,
		MetricLength:        1,
		LabelLength:         1,
		MaxSeriesCount:      1000,
		MinSeriesCount:      0,
		SeriesOperationMode: disabledOpMode,

		PartialSeriesChurnPercent:  50, // windowTargetCount = 50
		PartialSeriesChurnInterval: 8,
		PartialSeriesChurnStep:     2, // steps = 4
		RuleGroupSize:              10,
		RuleEvalInterval:           60,
	}
	assert.NoError(t, testCfg.Validate())

	reg := prometheus.NewRegistry()
	coll := NewCollector(testCfg)
	reg.MustRegister(coll)

	go coll.Run()
	t.Cleanup(func() {
		coll.Stop(nil)
	})

	// Poll continuously through the whole window (plus a small buffer past its
	// end) instead of sleeping to fixed checkpoints, so the assertions are
	// robust to goroutine/ticker scheduling jitter: the churned count must
	// never exceed the window target and must never decrease, and must reach
	// exactly the target by the end of the window.
	deadline := time.Now().Add(9 * time.Second)
	prev := -1
	for time.Now().Before(deadline) {
		cur := churnedCount(t, reg)
		assert.LessOrEqual(t, cur, 50, "churned count must never exceed the window target")
		assert.GreaterOrEqual(t, cur, prev, "churned count must never decrease within a window")
		prev = cur
		time.Sleep(200 * time.Millisecond)
	}
	assert.Equal(t, 50, churnedCount(t, reg), "expected the full window target to be churned by the end of the window")
}

func TestRunMetrics_PartialSeriesChurn_LabelPresentWithOtherLabels(t *testing.T) {
	testCfg := Config{
		GaugeMetricCount:    1,
		LabelCount:          2,
		SeriesCount:         20,
		MetricLength:        1,
		LabelLength:         1,
		MaxSeriesCount:      100,
		MinSeriesCount:      0,
		SeriesOperationMode: disabledOpMode,
		ConstLabels:         []string{"constLabel=test"},

		PartialSeriesChurnPercent:  100,
		PartialSeriesChurnInterval: 1,
		PartialSeriesChurnStep:     1,
		RuleGroupSize:              10,
		RuleEvalInterval:           60,
	}
	assert.NoError(t, testCfg.Validate())

	reg := prometheus.NewRegistry()
	coll := NewCollector(testCfg)
	reg.MustRegister(coll)

	go coll.Run()
	t.Cleanup(func() {
		coll.Stop(nil)
	})

	time.Sleep(2 * time.Second)

	metricsFamilies, err := reg.Gather()
	require.NoError(t, err)
	require.NotEmpty(t, metricsFamilies)
	for _, mf := range metricsFamilies {
		for _, m := range mf.Metric {
			labelMap := make(map[string]string)
			for _, l := range m.GetLabel() {
				labelMap[l.GetName()] = l.GetValue()
			}
			assert.Equal(t, "test", labelMap["constLabel"])
			assert.Contains(t, labelMap, "label_key_k_0")
			assert.Contains(t, labelMap, "series_id")
			assert.Contains(t, labelMap, "cycle_id")
			assert.Contains(t, labelMap, "churn_generation")
			assert.NotEqual(t, "0", labelMap["churn_generation"])
		}
	}
}

func TestRunMetrics_SeriesIntervalAndPartialChurn_Independent(t *testing.T) {
	testCfg := Config{
		GaugeMetricCount:    1,
		LabelCount:          1,
		SeriesCount:         100,
		MetricLength:        1,
		LabelLength:         1,
		MaxSeriesCount:      1000,
		MinSeriesCount:      0,
		SeriesOperationMode: disabledOpMode,

		SeriesInterval: 1, // Full cycle_id churn every second, independent of the mechanism below.

		PartialSeriesChurnPercent:  50,
		PartialSeriesChurnInterval: 8,
		PartialSeriesChurnStep:     2,
		RuleGroupSize:              10,
		RuleEvalInterval:           60,
	}
	assert.NoError(t, testCfg.Validate())

	reg := prometheus.NewRegistry()
	coll := NewCollector(testCfg)
	reg.MustRegister(coll)

	go coll.Run()
	t.Cleanup(func() {
		coll.Stop(nil)
	})

	cycleID := -1
	prevChurned := -1
	deadline := time.Now().Add(9 * time.Second)
	for time.Now().Before(deadline) {
		gotCycleID := currentCycleID(t, reg)
		assert.GreaterOrEqual(t, gotCycleID, cycleID, "cycle_id must never go backwards")
		cycleID = gotCycleID

		churned := churnedCount(t, reg)
		assert.LessOrEqual(t, churned, 50, "churned count must never exceed the window target")
		assert.GreaterOrEqual(t, churned, prevChurned, "churned count must never decrease within a window")
		prevChurned = churned

		time.Sleep(300 * time.Millisecond)
	}
	require.Greater(t, cycleID, 0, "cycle_id should have advanced at least once via --series-interval")
	assert.Equal(t, 50, churnedCount(t, reg), "partial series churn should reach its window target independently of --series-interval")
}

// gatherGaugeValuesBySeriesID returns the current gauge values keyed by
// their series_id label, for comparing value sequences across collectors.
func gatherGaugeValuesBySeriesID(t *testing.T, registry *prometheus.Registry) map[string]float64 {
	t.Helper()

	metricsFamilies, err := registry.Gather()
	assert.NoError(t, err)

	values := make(map[string]float64)
	for _, mf := range metricsFamilies {
		if mf.GetType() != io_prometheus_client.MetricType_GAUGE {
			continue
		}
		for _, m := range mf.Metric {
			var seriesID string
			for _, l := range m.GetLabel() {
				if l.GetName() == "series_id" {
					seriesID = l.GetValue()
				}
			}
			values[seriesID] = m.GetGauge().GetValue()
		}
	}
	return values
}

func TestNewCollector_SeedDeterminism(t *testing.T) {
	newCfg := func() Config {
		return Config{
			GaugeMetricCount:    1,
			LabelCount:          1,
			SeriesCount:         5,
			MetricLength:        1,
			LabelLength:         1,
			MaxSeriesCount:      10,
			MinSeriesCount:      0,
			SeriesOperationMode: disabledOpMode,

			PartialSeriesChurnInterval: 7200,
			PartialSeriesChurnStep:     30,
			RuleGroupSize:              10,
			RuleEvalInterval:           60,

			Seed: 42,
		}
	}

	run := func() map[string]float64 {
		cfg := newCfg()
		require.NoError(t, cfg.Validate())

		reg := prometheus.NewRegistry()
		coll := NewCollector(cfg)
		reg.MustRegister(coll)

		go coll.Run()
		t.Cleanup(func() {
			coll.Stop(nil)
		})

		time.Sleep(300 * time.Millisecond)
		return gatherGaugeValuesBySeriesID(t, reg)
	}

	first := run()
	second := run()
	require.NotEmpty(t, first)
	assert.Equal(t, first, second, "two collectors with the same non-zero --seed must produce identical value sequences")
}

func TestCollectorLabels(t *testing.T) {
	testCfg := Config{
		GaugeMetricCount:    1,
		LabelCount:          2,
		SeriesCount:         100,
		MaxSeriesCount:      30,
		MinSeriesCount:      10,
		SpikeMultiplier:     1.5,
		MetricLength:        1,
		LabelLength:         1,
		SeriesOperationMode: spikeOpMode,
		ConstLabels:         []string{"constLabel=test"},

		PartialSeriesChurnInterval: 7200,
		PartialSeriesChurnStep:     30,
		RuleGroupSize:              10,
		RuleEvalInterval:           60,
	}

	assert.NoError(t, testCfg.Validate())

	reg := prometheus.NewRegistry()
	col := NewCollector(testCfg)
	reg.MustRegister(col)

	go col.Run()
	t.Cleanup(func() {
		col.Stop(nil)
	})

	select {
	case <-col.updateNotifyCh:
		metricsFamilies, err := reg.Gather()
		assert.NotEmpty(t, metricsFamilies)
		assert.NoError(t, err)

		for _, mf := range metricsFamilies {
			for _, m := range mf.Metric {
				labels := m.GetLabel()
				labelMap := make(map[string]string)
				for _, l := range labels {
					labelMap[l.GetName()] = l.GetValue()
				}
				assert.Equal(t, "test", labelMap["constLabel"])
				assert.Contains(t, labelMap, "label_key_k_0")
				assert.Contains(t, labelMap, "label_key_k_1")
				assert.Contains(t, labelMap, "series_id")
				assert.Contains(t, labelMap, "cycle_id")
			}
		}
	case <-time.After(2 * time.Second):
		t.Fail()
	}
}
