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
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

// mustParseRuleGroups unmarshals generated rule YAML back into our own
// ruleGroups struct — a structural round-trip check. We deliberately don't
// use a real PromQL parser here: both rulefmt and promql/parser transitively
// pull in the full query engine (rulefmt via annotation-template
// validation, promql/parser's ast.go via the storage package), dragging in
// ~30 unrelated indirect modules (k8s.io/client-go, OpenTelemetry, etc.) for
// a YAML-serialization feature — measured +6.8MB/+35% binary size. See
// design.md's Risk entry.
func mustParseRuleGroups(t *testing.T, out []byte) ruleGroups {
	t.Helper()
	var groups ruleGroups
	require.NoError(t, yaml.Unmarshal(out, &groups))
	return groups
}

func allExprs(groups ruleGroups) []string {
	var exprs []string
	for _, g := range groups.Groups {
		for _, r := range g.Rules {
			exprs = append(exprs, r.Expr)
		}
	}
	return exprs
}

func allRules(groups ruleGroups) []rule {
	var rules []rule
	for _, g := range groups.Groups {
		rules = append(rules, g.Rules...)
	}
	return rules
}

func TestGenerateRules_DefaultCountsProduceEmptyGroups(t *testing.T) {
	cfg := Config{
		GaugeMetricCount: 10,
		LabelCount:       1,
		MetricLength:     1,
		LabelLength:      1,
		RuleGroupSize:    10,
		RuleEvalInterval: 60,
		// RecordingRuleCount, AlertingRuleCount left at their zero value (0).
	}
	out, err := GenerateRules(cfg)
	require.NoError(t, err)

	groups := mustParseRuleGroups(t, out)
	assert.Empty(t, groups.Groups)
}

func TestGenerateRules_RecordingTemplatesPerType(t *testing.T) {
	cfg := Config{
		GaugeMetricCount:           1,
		CounterMetricCount:         1,
		HistogramMetricCount:       1,
		NativeHistogramMetricCount: 1,
		SummaryMetricCount:         1,
		SummaryObjectives:          2,
		LabelCount:                 1,
		MetricLength:               1,
		LabelLength:                1,
		RuleGroupSize:              10,
		RuleEvalInterval:           60,
		RecordingRuleCount:         5,
	}
	out, err := GenerateRules(cfg)
	require.NoError(t, err)

	rules := allRules(mustParseRuleGroups(t, out))
	require.Len(t, rules, 5)

	var gaugeExpr, counterExpr, histogramExpr, nativeHistogramExpr, summaryExpr string
	for _, r := range rules {
		switch {
		case strings.Contains(r.Expr, "_bucket[5m]"):
			histogramExpr = r.Expr
		case strings.Contains(r.Expr, "histogram_quantile"):
			nativeHistogramExpr = r.Expr
		case strings.Contains(r.Expr, "quantile="):
			summaryExpr = r.Expr
		case strings.Contains(r.Expr, "rate("):
			counterExpr = r.Expr
		default:
			gaugeExpr = r.Expr
		}
	}

	assert.Contains(t, gaugeExpr, "sum by (")
	assert.Contains(t, counterExpr, "rate(")
	assert.Contains(t, counterExpr, "avalanche_counter_metric")
	assert.Contains(t, histogramExpr, "histogram_quantile(0.99, sum by (le")
	assert.Contains(t, histogramExpr, "_bucket[5m]")
	assert.Contains(t, nativeHistogramExpr, "histogram_quantile(0.99, sum by (")
	assert.NotContains(t, nativeHistogramExpr, "_bucket")
	assert.Contains(t, summaryExpr, `quantile="0.5"`) // SummaryObjectives=2 -> quantiles {0.5, 0.99}, first is 0.5.
}

func TestGenerateRules_RecordingRulesCoverAllPresentKinds(t *testing.T) {
	cfg := Config{
		GaugeMetricCount:           500,
		HistogramMetricCount:       1,
		NativeHistogramMetricCount: 1,
		SummaryMetricCount:         1,
		SummaryObjectives:          2,
		LabelCount:                 1,
		MetricLength:               1,
		LabelLength:                1,
		RuleGroupSize:              100,
		RuleEvalInterval:           60,
		RecordingRuleCount:         10,
	}
	out, err := GenerateRules(cfg)
	require.NoError(t, err)

	exprs := allExprs(mustParseRuleGroups(t, out))
	require.Len(t, exprs, 10)

	var hasHistogram, hasNativeHistogram, hasSummary, hasGauge bool
	for _, e := range exprs {
		switch {
		case strings.Contains(e, "_bucket[5m]"):
			hasHistogram = true
		case strings.Contains(e, "histogram_quantile"):
			hasNativeHistogram = true
		case strings.Contains(e, "quantile="):
			hasSummary = true
		default:
			hasGauge = true
		}
	}
	assert.True(t, hasHistogram, "expected at least one classic histogram recording rule among the first 10")
	assert.True(t, hasNativeHistogram, "expected at least one native histogram recording rule among the first 10")
	assert.True(t, hasSummary, "expected at least one summary recording rule among the first 10")
	assert.True(t, hasGauge, "expected at least one gauge recording rule among the first 10")
}

func TestGenerateRules_SummaryQuantileMatchesConfiguredObjectives(t *testing.T) {
	cfg := Config{
		SummaryMetricCount: 1,
		SummaryObjectives:  3, // quantiles {0.33, 0.66, 0.99} -- does NOT include 0.5.
		LabelCount:         1,
		MetricLength:       1,
		LabelLength:        1,
		RuleGroupSize:      10,
		RuleEvalInterval:   60,
		RecordingRuleCount: 1,
	}
	out, err := GenerateRules(cfg)
	require.NoError(t, err)

	exprs := allExprs(mustParseRuleGroups(t, out))
	require.Len(t, exprs, 1)
	assert.Contains(t, exprs[0], `quantile="0.33"`)
	assert.NotContains(t, exprs[0], `quantile="0.5"`)
}

func TestGenerateRules_AlertingMatchesItsRecordingRuleType(t *testing.T) {
	cfg := Config{
		GaugeMetricCount:   1,
		CounterMetricCount: 1,
		LabelCount:         1,
		MetricLength:       1,
		LabelLength:        1,
		RuleGroupSize:      100, // keep everything in one group, in generation order.
		RuleEvalInterval:   60,
		RecordingRuleCount: 2, // one gauge, one counter recording rule.
		AlertingRuleCount:  5, // 5 % 2 != 0, so alert index and recording index diverge without the fix.
	}
	out, err := GenerateRules(cfg)
	require.NoError(t, err)

	rules := allRules(mustParseRuleGroups(t, out))
	require.Len(t, rules, 2+5)
	recording := rules[:2]
	alerting := rules[2:]

	for i, a := range alerting {
		wantRecord := recording[i%2].Record
		require.True(t, strings.HasPrefix(a.Expr, wantRecord+" > "), "alert %d expr %q must reference %q", i, a.Expr, wantRecord)

		wantKind := "Gauge"
		if i%2 == 1 {
			wantKind = "Counter"
		}
		assert.Contains(t, a.Alert, wantKind, "alert %d name must match the kind of the recording rule it targets", i)
	}
}

func TestGenerateRules_CapsAtUniqueMetricFamilies(t *testing.T) {
	cfg := Config{
		GaugeMetricCount:   3,
		LabelCount:         1,
		MetricLength:       1,
		LabelLength:        1,
		RuleGroupSize:      10,
		RuleEvalInterval:   60,
		RecordingRuleCount: 1000,
	}
	out, err := GenerateRules(cfg)
	require.NoError(t, err)

	rules := allRules(mustParseRuleGroups(t, out))
	assert.Len(t, rules, 3)
}

func TestGenerateRules_GroupSizeDistribution(t *testing.T) {
	cfg := Config{
		GaugeMetricCount:   25,
		LabelCount:         1,
		MetricLength:       1,
		LabelLength:        1,
		RuleGroupSize:      10,
		RuleEvalInterval:   60,
		RecordingRuleCount: 25,
	}
	out, err := GenerateRules(cfg)
	require.NoError(t, err)

	groups := mustParseRuleGroups(t, out).Groups
	require.Len(t, groups, 3)
	assert.Len(t, groups[0].Rules, 10)
	assert.Len(t, groups[1].Rules, 10)
	assert.Len(t, groups[2].Rules, 5)
}

func TestGenerateRules_AlertingRuleRequiredFields(t *testing.T) {
	cfg := Config{
		GaugeMetricCount:  5,
		LabelCount:        1,
		MetricLength:      1,
		LabelLength:       1,
		RuleGroupSize:     10,
		RuleEvalInterval:  60,
		AlertingRuleCount: 5,
	}
	out, err := GenerateRules(cfg)
	require.NoError(t, err)

	rules := allRules(mustParseRuleGroups(t, out))
	require.Len(t, rules, 5)
	for _, r := range rules {
		assert.NotEmpty(t, r.Alert)
		assert.NotEmpty(t, r.Expr)
		assert.NotEmpty(t, r.For)
		assert.NotEmpty(t, r.Labels)
		assert.NotEmpty(t, r.Annotations)
	}
}

func TestGenerateRules_NoLabelsProducesValidExpr(t *testing.T) {
	cfg := Config{
		GaugeMetricCount:           1,
		CounterMetricCount:         1,
		HistogramMetricCount:       1,
		NativeHistogramMetricCount: 1,
		SummaryMetricCount:         1,
		SummaryObjectives:          2,
		LabelCount:                 0, // no labels at all.
		MetricLength:               1,
		LabelLength:                1,
		RuleGroupSize:              10,
		RuleEvalInterval:           60,
		RecordingRuleCount:         5,
	}
	out, err := GenerateRules(cfg)
	require.NoError(t, err)

	for _, e := range allExprs(mustParseRuleGroups(t, out)) {
		assert.NotContains(t, e, ", )", "expr must not have a dangling comma when there are no labels: %q", e)
		assert.NotContains(t, e, "by ()", "expr must omit the by(...) clause entirely rather than emit an empty one: %q", e)
	}
}

func TestGenerateRules_NoExternalOrReplicaLabels(t *testing.T) {
	cfg := Config{
		GaugeMetricCount:   10,
		LabelCount:         2,
		MetricLength:       1,
		LabelLength:        1,
		RuleGroupSize:      10,
		RuleEvalInterval:   60,
		RecordingRuleCount: 10,
		AlertingRuleCount:  10,
	}
	first, err := GenerateRules(cfg)
	require.NoError(t, err)
	second, err := GenerateRules(cfg)
	require.NoError(t, err)

	assert.Equal(t, first, second, "GenerateRules must be deterministic — independent of how many replicas call it")

	for _, e := range allExprs(mustParseRuleGroups(t, first)) {
		assert.NotContains(t, e, "pod")
		assert.NotContains(t, e, "instance")
		assert.NotContains(t, e, "job")
	}
}

func TestAlertThreshold_CounterScalesWithValueInterval(t *testing.T) {
	assert.InDelta(t, 50.0/30.0, alertThreshold("counter", Config{ValueInterval: 30}), 1e-9)
	assert.InDelta(t, 50.0, alertThreshold("counter", Config{ValueInterval: 0}), 1e-9) // clamped to interval=1.
	assert.Equal(t, 80.0, alertThreshold("gauge", Config{}))
	assert.Equal(t, 80.0, alertThreshold("histogram", Config{}))
	assert.Equal(t, 80.0, alertThreshold("native_histogram", Config{}))
	assert.Equal(t, 50.0, alertThreshold("summary", Config{}))
}
