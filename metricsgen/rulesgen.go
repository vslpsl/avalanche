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
	"strconv"

	"go.yaml.in/yaml/v3"
)

// ruleGroups/ruleGroup/rule mirror the public Prometheus rule file YAML
// format (https://prometheus.io/docs/prometheus/latest/configuration/recording_rules/)
// field-for-field. Deliberately hand-rolled instead of importing
// github.com/prometheus/prometheus/model/rulefmt: that package pulls in the
// full PromQL query engine (for annotation-template validation), which
// transitively drags in a huge dependency tree unrelated to this feature
// (k8s.io/client-go, OpenTelemetry, etc. — measured +6.8MB/+35% binary size
// for a handful of YAML struct tags). avalanche otherwise only depends on
// the small, self-contained prometheus/prometheus/prompb package — keep it
// that way. The Prometheus rule file format itself is a stable, public
// contract, safe to mirror directly.
type ruleGroups struct {
	Groups []ruleGroup `yaml:"groups"`
}

type ruleGroup struct {
	Name     string `yaml:"name"`
	Interval string `yaml:"interval,omitempty"`
	Rules    []rule `yaml:"rules"`
}

type rule struct {
	Record      string            `yaml:"record,omitempty"`
	Alert       string            `yaml:"alert,omitempty"`
	Expr        string            `yaml:"expr"`
	For         string            `yaml:"for,omitempty"`
	Labels      map[string]string `yaml:"labels,omitempty"`
	Annotations map[string]string `yaml:"annotations,omitempty"`
}

// metricInfo is a deterministically generated metric (name + kind), used
// only by the rule generator below.
type metricInfo struct {
	name string
	kind string // "gauge", "counter", "histogram", "native_histogram", "summary"
}

// kindDisplay gives each kind a CamelCase display form, used in alert names.
var kindDisplay = map[string]string{
	"gauge":            "Gauge",
	"counter":          "Counter",
	"histogram":        "Histogram",
	"native_histogram": "NativeHistogram",
	"summary":          "Summary",
}

// generatedMetrics is a pure function: the list of metrics this Config will
// produce at startup (metricCycle=0), without ever touching a live
// Collector. It interleaves metric kinds round-robin (gauge, counter,
// histogram, native_histogram, summary, then repeat) rather than emitting
// them in contiguous blocks, so that a short prefix of this list (as picked
// by --recording-rule-count/--alerting-rule-count) still covers every
// present kind instead of only the first-configured one.
func generatedMetrics(cfg Config) []metricInfo {
	kinds := []struct {
		kind   string
		count  int
		nameFn func(Config, int, int) string
	}{
		{"gauge", cfg.GaugeMetricCount, gaugeMetricName},
		{"counter", cfg.CounterMetricCount, counterMetricName},
		{"histogram", cfg.HistogramMetricCount, histogramMetricName},
		{"native_histogram", cfg.NativeHistogramMetricCount, nativeHistogramMetricName},
		{"summary", cfg.SummaryMetricCount, summaryMetricName},
	}

	var metrics []metricInfo
	for id := 0; ; id++ {
		emittedAny := false
		for _, k := range kinds {
			if id < k.count {
				metrics = append(metrics, metricInfo{name: k.nameFn(cfg, 0, id), kind: k.kind})
				emittedAny = true
			}
		}
		if !emittedAny {
			break
		}
	}
	return metrics
}

// summaryQuantiles mirrors the quantile-level computation in
// recreateMetrics's summary-objectives block (serve.go) — keep the two in
// sync if that formula ever changes. Needed so the summary recording-rule
// template can reference a quantile that actually exists on the series,
// instead of a hardcoded value that may not be configured.
func summaryQuantiles(cfg Config) []float64 {
	if cfg.SummaryObjectives <= 0 {
		return nil
	}
	quantiles := make([]float64, 0, cfg.SummaryObjectives)
	parts := 100 / cfg.SummaryObjectives
	for i := 0; i < cfg.SummaryObjectives; i++ {
		q := parts * (i + 1)
		if q == 100 {
			q = 99
		}
		quantiles = append(quantiles, float64(q)/100.0)
	}
	return quantiles
}

// aggWrap wraps inner in an aggregation function, grouping by label unless
// label is empty (in which case the by(...) clause is omitted entirely,
// rather than emitting a malformed "by ()"/"by (le, )").
func aggWrap(fn, label, inner string) string {
	if label == "" {
		return fmt.Sprintf("%s(%s)", fn, inner)
	}
	return fmt.Sprintf("%s by (%s) (%s)", fn, label, inner)
}

// recordingRuleFor builds the recording rule for metric m, following the
// fixed per-kind templates (design.md Decision 3). label is the avalanche
// label key to group by (own label_key_... keys only — never pod/instance/
// job — so the rule's result is independent of how many avalanche replicas
// with the same config exist).
func recordingRuleFor(cfg Config, m metricInfo, label string, i int) rule {
	var expr string
	switch m.kind {
	case "gauge":
		expr = aggWrap("sum", label, m.name)
	case "counter":
		expr = aggWrap("sum", label, fmt.Sprintf("rate(%s[5m])", m.name))
	case "histogram":
		byLabels := "le"
		if label != "" {
			byLabels = "le, " + label
		}
		expr = fmt.Sprintf("histogram_quantile(0.99, sum by (%s) (rate(%s_bucket[5m])))", byLabels, m.name)
	case "native_histogram":
		expr = fmt.Sprintf("histogram_quantile(0.99, %s)", aggWrap("sum", label, fmt.Sprintf("rate(%s[5m])", m.name)))
	case "summary":
		var selector string
		if quantiles := summaryQuantiles(cfg); len(quantiles) > 0 {
			q := strconv.FormatFloat(quantiles[0], 'g', -1, 64)
			selector = fmt.Sprintf("%s{quantile=%q}", m.name, q)
		} else {
			// No quantile objectives configured (--summary-metric-objective-count=0):
			// no quantile-labeled series exist, fall back to the always-present _sum series.
			selector = fmt.Sprintf("rate(%s_sum[5m])", m.name)
		}
		expr = aggWrap("avg", label, selector)
	default:
		panic("rulesgen: unknown metric kind " + m.kind)
	}
	return rule{
		Record: fmt.Sprintf("avalanche:recording_rule_%d", i),
		Expr:   expr,
	}
}

// alertThreshold picks a simple, documented threshold heuristic per kind.
// All metric types are fed raw values via c.valGen.Intn(100) (see
// cycleValues in serve.go), i.e. range [0,99), regardless of type — gauge,
// histogram and native_histogram quantiles are thresholded directly against
// that range. Counters instead accumulate via repeated Add() on every
// --value-interval tick, so a rate(...[5m]) scales inversely with
// --value-interval — the threshold is derived from cfg.ValueInterval rather
// than hardcoded. These are starting points for a load test, not a
// guarantee of a "meaningful" alert (see README).
func alertThreshold(kind string, cfg Config) float64 {
	switch kind {
	case "counter":
		interval := cfg.ValueInterval
		if interval < 1 {
			interval = 1
		}
		return 50.0 / float64(interval)
	case "summary":
		return 50
	default: // gauge, histogram, native_histogram
		return 80
	}
}

// alertingRuleFor builds an alerting rule over baseExpr (a recording rule's
// name, or a metric name directly when no recording rules were generated).
// src is the metricInfo baseExpr was actually derived from — the caller
// MUST NOT pass a metricInfo unrelated to baseExpr (e.g. picked by a
// different index), or the alert's name/threshold will silently mismatch
// its expr.
func alertingRuleFor(cfg Config, src metricInfo, baseExpr string, i int) rule {
	threshold := alertThreshold(src.kind, cfg)
	name := fmt.Sprintf("Avalanche%sAbove%d", kindDisplay[src.kind], i)
	return rule{
		Alert: name,
		Expr:  fmt.Sprintf("%s > %s", baseExpr, strconv.FormatFloat(threshold, 'g', -1, 64)),
		For:   "5m",
		Labels: map[string]string{
			"severity": "warning",
		},
		Annotations: map[string]string{
			"summary": fmt.Sprintf("%s is above threshold", name),
		},
	}
}

// groupRules distributes rules into groups of at most groupSize, each with
// interval set to evalIntervalSeconds.
func groupRules(rules []rule, groupSize, evalIntervalSeconds int) []ruleGroup {
	if len(rules) == 0 {
		return nil
	}
	if groupSize <= 0 {
		groupSize = len(rules)
	}
	interval := fmt.Sprintf("%ds", evalIntervalSeconds)
	groups := make([]ruleGroup, 0, (len(rules)+groupSize-1)/groupSize)
	for i := 0; i < len(rules); i += groupSize {
		end := i + groupSize
		if end > len(rules) {
			end = len(rules)
		}
		groups = append(groups, ruleGroup{
			Name:     fmt.Sprintf("avalanche-%d", len(groups)),
			Interval: interval,
			Rules:    rules[i:end],
		})
	}
	return groups
}

// GenerateRules builds the Prometheus rule groups YAML for the given
// Config. It's a pure function: the result depends only on Config, never on
// live Collector/churn state, and never on how many other avalanche
// replicas with the same config are running (see the "Независимость от
// количества реплик" requirement in the rule-generation spec) — expressions
// only ever group/filter by avalanche's own label_key_... keys, never by
// external labels like pod/instance/job.
func GenerateRules(cfg Config) ([]byte, error) {
	metrics := generatedMetrics(cfg)
	labelKeys := buildLabelKeys(cfg)
	pickLabel := func(i int) string {
		if len(labelKeys) == 0 {
			return ""
		}
		return labelKeys[i%len(labelKeys)]
	}

	recordingN := cfg.RecordingRuleCount
	if recordingN > len(metrics) {
		recordingN = len(metrics) // Cap: never generate more recording rules than unique metric families.
	}
	alertingN := cfg.AlertingRuleCount
	if recordingN == 0 && alertingN > len(metrics) {
		// Only cap by metric-family count when alerts reference metrics directly
		// (metrics[i], below) — once recordingN>0, alerts reference recording
		// rules via i%recordingN and can validly outnumber the metric families.
		alertingN = len(metrics)
	}

	recording := make([]rule, 0, recordingN)
	recordingSrc := make([]metricInfo, 0, recordingN) // parallel to `recording` — which metricInfo each rule was built from.
	for i := 0; i < recordingN; i++ {
		recording = append(recording, recordingRuleFor(cfg, metrics[i], pickLabel(i), i))
		recordingSrc = append(recordingSrc, metrics[i])
	}

	alerting := make([]rule, 0, alertingN)
	for i := 0; i < alertingN; i++ {
		var src metricInfo
		var baseExpr string
		if recordingN > 0 {
			// Must use the SAME index into recordingSrc/recording, not metrics[i]:
			// alertingN and recordingN are independent flags, so i%recordingN is the
			// only index that keeps the alert's name/threshold consistent with the
			// expr it actually points at.
			src = recordingSrc[i%recordingN]
			baseExpr = recording[i%recordingN].Record
		} else {
			src = metrics[i]
			baseExpr = metrics[i].name
		}
		alerting = append(alerting, alertingRuleFor(cfg, src, baseExpr, i))
	}

	all := make([]rule, 0, len(recording)+len(alerting))
	all = append(all, recording...)
	all = append(all, alerting...)

	groups := groupRules(all, cfg.RuleGroupSize, cfg.RuleEvalInterval)
	if groups == nil {
		groups = []ruleGroup{}
	}

	out, err := yaml.Marshal(&ruleGroups{Groups: groups})
	if err != nil {
		return nil, fmt.Errorf("marshal rule groups: %w", err)
	}
	return out, nil
}
