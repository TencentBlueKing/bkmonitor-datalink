// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package hpa emits kube_hpa_* metrics for HorizontalPodAutoscaler objects,
// reading them from the autoscaling/v2 API.
//
// Background: the bundled kube-state-metrics v1.9.7 reads HPAs from
// autoscaling/v2beta1, which Kubernetes removed in 1.25. On clusters >= 1.25 it
// therefore produces no kube_hpa_* metrics. This collector keeps the exact same
// metric names, labels and semantics as kube-state-metrics v1.9.7 but reads from
// autoscaling/v2 (served on 1.23+), so downstream strategies and dashboards keep
// working unchanged. This is a clean-room reimplementation; no kube-state-metrics
// source code is copied.
package hpa

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	"k8s.io/apimachinery/pkg/labels"
	listersv2 "k8s.io/client-go/listers/autoscaling/v2"
)

// Lister is the read side of the autoscaling/v2 HPA informer cache.
type Lister = listersv2.HorizontalPodAutoscalerLister

// Collector renders kube_hpa_* metrics from the local informer cache.
type Collector struct {
	lister Lister
}

// New returns a Collector backed by the given HPA lister.
func New(lister Lister) *Collector { return &Collector{lister: lister} }

// Help text, identical to kube-state-metrics v1.9.7.
const (
	helpGeneration      = "The generation observed by the HorizontalPodAutoscaler controller."
	helpMaxReplicas     = "Upper limit for the number of pods that can be set by the autoscaler; cannot be smaller than MinReplicas."
	helpMinReplicas     = "Lower limit for the number of pods that can be set by the autoscaler, default 1."
	helpCurrentReplicas = "Current number of replicas of pods managed by this autoscaler."
	helpDesiredReplicas = "Desired number of replicas of pods managed by this autoscaler."
	helpLabels          = "Kubernetes labels converted to Prometheus labels."
	helpCondition       = "The condition of this autoscaler."
	helpTargetMetric    = "The metric specifications used by this autoscaler when calculating the desired replica count."
)

// conditionStatuses mirrors kube-state-metrics: every condition is emitted as
// three series (true/false/unknown), each valued 1 only for the active status.
var conditionStatuses = []string{"true", "false", "unknown"}

// Write renders all kube_hpa_* metrics in Prometheus text exposition format.
func (c *Collector) Write(w io.Writer) error {
	hpas, err := c.lister.List(labels.Everything())
	if err != nil {
		return err
	}
	return writeMetrics(w, hpas)
}

// writeMetrics renders all kube_hpa_* metrics for the given HPAs. It is split out
// from Write so tests can exercise rendering with fixture objects.
func writeMetrics(w io.Writer, hpas []*autoscalingv2.HorizontalPodAutoscaler) error {
	sort.Slice(hpas, func(i, j int) bool {
		if hpas[i].Namespace != hpas[j].Namespace {
			return hpas[i].Namespace < hpas[j].Namespace
		}
		return hpas[i].Name < hpas[j].Name
	})

	mw := &metricWriter{w: w}

	mw.help("kube_hpa_metadata_generation", helpGeneration)
	for _, h := range hpas {
		mw.sample("kube_hpa_metadata_generation", base(h), nil, float64(h.Generation))
	}

	mw.help("kube_hpa_spec_max_replicas", helpMaxReplicas)
	for _, h := range hpas {
		mw.sample("kube_hpa_spec_max_replicas", base(h), nil, float64(h.Spec.MaxReplicas))
	}

	mw.help("kube_hpa_spec_min_replicas", helpMinReplicas)
	for _, h := range hpas {
		if h.Spec.MinReplicas != nil {
			mw.sample("kube_hpa_spec_min_replicas", base(h), nil, float64(*h.Spec.MinReplicas))
		}
	}

	mw.help("kube_hpa_status_current_replicas", helpCurrentReplicas)
	for _, h := range hpas {
		mw.sample("kube_hpa_status_current_replicas", base(h), nil, float64(h.Status.CurrentReplicas))
	}

	mw.help("kube_hpa_status_desired_replicas", helpDesiredReplicas)
	for _, h := range hpas {
		mw.sample("kube_hpa_status_desired_replicas", base(h), nil, float64(h.Status.DesiredReplicas))
	}

	mw.help("kube_hpa_labels", helpLabels)
	for _, h := range hpas {
		keys, vals := labelPairs(h.Labels)
		mw.sample("kube_hpa_labels", base(h), &labelSet{keys: keys, vals: vals}, 1)
	}

	mw.help("kube_hpa_status_condition", helpCondition)
	for _, h := range hpas {
		for _, cond := range h.Status.Conditions {
			active := strings.ToLower(string(cond.Status))
			for _, st := range conditionStatuses {
				v := 0.0
				if active == st {
					v = 1.0
				}
				extra := &labelSet{keys: []string{"condition", "status"}, vals: []string{string(cond.Type), st}}
				mw.sample("kube_hpa_status_condition", base(h), extra, v)
			}
		}
	}

	mw.help("kube_hpa_spec_target_metric", helpTargetMetric)
	for _, h := range hpas {
		for _, m := range h.Spec.Metrics {
			name, target, ok := metricSpecTarget(m)
			if !ok {
				continue
			}
			for _, tv := range targetTypeValues(target) {
				extra := &labelSet{
					keys: []string{"metric_name", "metric_target_type"},
					vals: []string{name, tv.typ},
				}
				mw.sample("kube_hpa_spec_target_metric", base(h), extra, tv.val)
			}
		}
	}

	return mw.err
}

// targetTypeValue is one (metric_target_type, value) pair derived from a v2
// MetricTarget for kube_hpa_spec_target_metric.
type targetTypeValue struct {
	typ string
	val float64
}

// metricSpecTarget extracts the metric name and target from a v2 MetricSpec,
// matching kube-state-metrics v1.9.7's per-source-type handling. ok is false for
// a source v1.9.7 did not emit (ContainerResource; see the case below) and for a
// malformed (nil) source struct or an unknown type.
func metricSpecTarget(m autoscalingv2.MetricSpec) (name string, target autoscalingv2.MetricTarget, ok bool) {
	switch m.Type {
	case autoscalingv2.ResourceMetricSourceType:
		if m.Resource == nil {
			return "", autoscalingv2.MetricTarget{}, false
		}
		return string(m.Resource.Name), m.Resource.Target, true
	case autoscalingv2.ContainerResourceMetricSourceType:
		// Not emitted. kube-state-metrics v1.9.7 read autoscaling/v2beta1, which has
		// no ContainerResource source, so there is no v1.9.7 series to match. It is
		// also unsafe to emit here: kube_hpa_spec_target_metric has no container
		// label, so two ContainerResource targets differing only by container -- e.g.
		// the old/new pair the autoscaling docs recommend keeping during a container
		// rename -- would collide into duplicate, conflicting samples.
		return "", autoscalingv2.MetricTarget{}, false
	case autoscalingv2.PodsMetricSourceType:
		if m.Pods == nil {
			return "", autoscalingv2.MetricTarget{}, false
		}
		return m.Pods.Metric.Name, m.Pods.Target, true
	case autoscalingv2.ObjectMetricSourceType:
		if m.Object == nil {
			return "", autoscalingv2.MetricTarget{}, false
		}
		return m.Object.Metric.Name, m.Object.Target, true
	case autoscalingv2.ExternalMetricSourceType:
		if m.External == nil {
			return "", autoscalingv2.MetricTarget{}, false
		}
		return m.External.Metric.Name, m.External.Target, true
	default:
		return "", autoscalingv2.MetricTarget{}, false
	}
}

// targetTypeValues renders a v2 MetricTarget into kube-state-metrics v1.9.7's
// (metric_target_type, value) pairs, one series per target field that is set, in
// v1.9.7's value/utilization/average order.
//
// The Quantity fields (Value, AverageValue) are gated on Quantity.AsInt64's ok
// flag exactly as v1.9.7 is: AsInt64 returns ok=false for any non-integer
// Quantity (e.g. "1500m"), and v1.9.7 then emits no series for it. Discarding ok
// would instead emit a misleading 0-valued series, so we skip when !ok.
// AverageUtilization is an *int32 (no Quantity), so it is emitted whenever set,
// matching v1.9.7's nil check.
//
// Note: v1.9.7 read a fixed set of target fields per source type; autoscaling/v2
// unifies them into one MetricTarget whose single set field matches Target.Type.
// This source-type-agnostic sweep reproduces v1.9.7 for the field a valid v2
// object actually sets, but cannot recreate v2beta1-only shapes (e.g. an
// averageValue Object, where v2beta1 also carried a separate required TargetValue
// row that has no v2 counterpart).
func targetTypeValues(t autoscalingv2.MetricTarget) []targetTypeValue {
	var out []targetTypeValue
	if t.Value != nil {
		if v, ok := t.Value.AsInt64(); ok {
			out = append(out, targetTypeValue{typ: "value", val: float64(v)})
		}
	}
	if t.AverageUtilization != nil {
		out = append(out, targetTypeValue{typ: "utilization", val: float64(*t.AverageUtilization)})
	}
	if t.AverageValue != nil {
		if v, ok := t.AverageValue.AsInt64(); ok {
			out = append(out, targetTypeValue{typ: "average", val: float64(v)})
		}
	}
	return out
}

// labelSet is an ordered list of label key/value pairs.
type labelSet struct {
	keys []string
	vals []string
}

// base returns the default labels every kube_hpa_* metric carries.
func base(h *autoscalingv2.HorizontalPodAutoscaler) *labelSet {
	return &labelSet{keys: []string{"namespace", "hpa"}, vals: []string{h.Namespace, h.Name}}
}

// labelPairs converts Kubernetes labels into sorted Prometheus label_<key> pairs.
//
// Like kube-state-metrics v1.9.7, distinct keys that sanitize to the same name
// (e.g. "app.name" and "app/name" both -> "label_app_name") are NOT de-duplicated;
// such a collision produces a kube_hpa_labels line with a repeated label name that
// Prometheus/VM reject. This matches the v1.9.7 baseline and is consciously kept
// (de-duping would diverge from it); the collision needs an unusual label set and
// does not arise for the common single-convention label keys.
func labelPairs(m map[string]string) ([]string, []string) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	pk := make([]string, 0, len(keys))
	pv := make([]string, 0, len(keys))
	for _, k := range keys {
		pk = append(pk, "label_"+sanitizeLabelName(k))
		pv = append(pv, m[k])
	}
	return pk, pv
}

// sanitizeLabelName maps a Kubernetes label key to a Prometheus label name,
// matching kube-state-metrics v1.9.7: every character outside [a-zA-Z0-9_] is
// replaced with '_', with no positional special-casing (a leading digit is kept,
// e.g. key "1app" -> "1app"; the "label_" prefix keeps the full name valid).
// Kubernetes validates label keys as an ASCII subset, so iterating by rune here
// is equivalent in practice to kube-state-metrics' byte-level regexp; a
// hypothetical non-ASCII key would collapse to one '_' per rune rather than per
// byte, but Kubernetes rejects such keys upstream anyway.
func sanitizeLabelName(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// metricWriter accumulates the first write error so callers check once.
type metricWriter struct {
	w   io.Writer
	err error
}

func (m *metricWriter) help(name, help string) {
	if m.err != nil {
		return
	}
	_, m.err = fmt.Fprintf(m.w, "# HELP %s %s\n# TYPE %s gauge\n", name, help, name)
}

func (m *metricWriter) sample(name string, base, extra *labelSet, value float64) {
	if m.err != nil {
		return
	}
	var b strings.Builder
	b.WriteString(name)
	b.WriteByte('{')
	first := true
	appendPairs := func(ls *labelSet) {
		if ls == nil {
			return
		}
		for i := range ls.keys {
			if !first {
				b.WriteByte(',')
			}
			first = false
			b.WriteString(ls.keys[i])
			b.WriteString(`="`)
			b.WriteString(escapeLabelValue(ls.vals[i]))
			b.WriteByte('"')
		}
	}
	appendPairs(base)
	appendPairs(extra)
	b.WriteByte('}')
	// Format with 'f' (not 'g') so large integer-valued gauges such as
	// metadata.generation render as plain integers, matching kube-state-metrics
	// v1.9.7 instead of switching to scientific notation at >= 1e6.
	_, m.err = fmt.Fprintf(m.w, "%s %s\n", b.String(), strconv.FormatFloat(value, 'f', -1, 64))
}

// escapeLabelValue escapes a label value per the Prometheus text format.
func escapeLabelValue(s string) string {
	if !strings.ContainsAny(s, "\\\"\n") {
		return s
	}
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`).Replace(s)
}
