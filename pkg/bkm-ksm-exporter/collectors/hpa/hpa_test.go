// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package hpa

import (
	"bytes"
	"strings"
	"testing"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func i32(v int32) *int32 { return &v }

func qty(s string) *resource.Quantity {
	q := resource.MustParse(s)
	return &q
}

func TestWriteMetrics(t *testing.T) {
	minR := i32(2)
	hpas := []*autoscalingv2.HorizontalPodAutoscaler{
		{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:  "ns1",
				Name:       "h1",
				Generation: 3,
				Labels:     map[string]string{"app": "web", "app.kubernetes.io/name": "svc", "1tier": "x"},
			},
			Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
				MaxReplicas: 5,
				MinReplicas: minR,
			},
			Status: autoscalingv2.HorizontalPodAutoscalerStatus{
				CurrentReplicas: 4,
				DesiredReplicas: 5,
				Conditions: []autoscalingv2.HorizontalPodAutoscalerCondition{
					{Type: "AbleToScale", Status: corev1.ConditionTrue},
				},
			},
		},
		{
			// No MinReplicas → kube_hpa_spec_min_replicas must be skipped for it.
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns0", Name: "h0"},
			Spec:       autoscalingv2.HorizontalPodAutoscalerSpec{MaxReplicas: 1},
		},
	}

	var buf bytes.Buffer
	if err := writeMetrics(&buf, hpas); err != nil {
		t.Fatalf("writeMetrics: %v", err)
	}
	out := buf.String()

	mustContain := []string{
		"# HELP kube_hpa_spec_max_replicas Upper limit",
		"# TYPE kube_hpa_spec_max_replicas gauge",
		`kube_hpa_metadata_generation{namespace="ns1",hpa="h1"} 3`,
		`kube_hpa_spec_max_replicas{namespace="ns1",hpa="h1"} 5`,
		`kube_hpa_spec_min_replicas{namespace="ns1",hpa="h1"} 2`,
		`kube_hpa_status_current_replicas{namespace="ns1",hpa="h1"} 4`,
		`kube_hpa_status_desired_replicas{namespace="ns1",hpa="h1"} 5`,
		`kube_hpa_labels{namespace="ns1",hpa="h1",label_1tier="x",label_app="web",label_app_kubernetes_io_name="svc"} 1`,
		`kube_hpa_status_condition{namespace="ns1",hpa="h1",condition="AbleToScale",status="true"} 1`,
		`kube_hpa_status_condition{namespace="ns1",hpa="h1",condition="AbleToScale",status="false"} 0`,
		`kube_hpa_status_condition{namespace="ns1",hpa="h1",condition="AbleToScale",status="unknown"} 0`,
		`kube_hpa_spec_max_replicas{namespace="ns0",hpa="h0"} 1`,
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Errorf("output missing line:\n%s\n--- full output ---\n%s", want, out)
		}
	}

	// h0 has no MinReplicas → its min_replicas series must be absent.
	if strings.Contains(out, `kube_hpa_spec_min_replicas{namespace="ns0",hpa="h0"}`) {
		t.Errorf("kube_hpa_spec_min_replicas should be skipped when MinReplicas is nil")
	}

	// ns0 sorts before ns1.
	if strings.Index(out, `hpa="h0"`) > strings.Index(out, `hpa="h1"`) {
		t.Errorf("expected ns0/h0 series to render before ns1/h1")
	}
}

func TestSanitizeLabelName(t *testing.T) {
	// Must match kube-state-metrics v1.9.7: regexp [^a-zA-Z0-9_] -> "_", with no
	// leading-digit special-casing (the "label_" prefix keeps the name valid).
	cases := map[string]string{
		"app":                    "app",
		"app.kubernetes.io/name": "app_kubernetes_io_name",
		"1bad":                   "1bad",
		"9":                      "9",
		"0_x":                    "0_x",
		"a-b.c":                  "a_b_c",
	}
	for in, want := range cases {
		if got := sanitizeLabelName(in); got != want {
			t.Errorf("sanitizeLabelName(%q)=%q want %q", in, got, want)
		}
	}
}

func TestEscapeLabelValue(t *testing.T) {
	if got := escapeLabelValue(`a"b\c` + "\n"); got != `a\"b\\c\n` {
		t.Errorf("escapeLabelValue mismatch: %q", got)
	}
	if got := escapeLabelValue("plain"); got != "plain" {
		t.Errorf("escapeLabelValue should pass through plain value, got %q", got)
	}
}

// TestLargeValueNoScientificNotation guards EXP-2: large integer-valued gauges
// such as metadata.generation must render as plain integers (matching
// kube-state-metrics v1.9.7), not switch to scientific notation at >= 1e6.
func TestLargeValueNoScientificNotation(t *testing.T) {
	hpas := []*autoscalingv2.HorizontalPodAutoscaler{
		{
			ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "big", Generation: 1234567},
			Spec:       autoscalingv2.HorizontalPodAutoscalerSpec{MaxReplicas: 1000000},
		},
	}
	var buf bytes.Buffer
	if err := writeMetrics(&buf, hpas); err != nil {
		t.Fatalf("writeMetrics: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `kube_hpa_metadata_generation{namespace="ns",hpa="big"} 1234567`) {
		t.Errorf("generation not rendered as plain integer; output:\n%s", out)
	}
	if !strings.Contains(out, `kube_hpa_spec_max_replicas{namespace="ns",hpa="big"} 1000000`) {
		t.Errorf("max_replicas not rendered as plain integer; output:\n%s", out)
	}
	if strings.Contains(out, "e+0") || strings.Contains(out, "E+0") {
		t.Errorf("output contains scientific notation:\n%s", out)
	}
}

// TestWriteTargetMetric covers kube_hpa_spec_target_metric for the autoscaling/v2
// MetricSpec source types kube-state-metrics v1.9.7 emits (Resource/Pods/Object/
// External): metric_name / metric_target_type / value conventions (utilization
// from AverageUtilization, value from Value, average from AverageValue), one
// series per target field that is set. ContainerResource is asserted to emit
// nothing (no v1.9.7 equivalent).
func TestWriteTargetMetric(t *testing.T) {
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "h"},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MaxReplicas: 5,
			Metrics: []autoscalingv2.MetricSpec{
				{
					Type: autoscalingv2.ResourceMetricSourceType,
					Resource: &autoscalingv2.ResourceMetricSource{
						Name:   corev1.ResourceCPU,
						Target: autoscalingv2.MetricTarget{Type: autoscalingv2.UtilizationMetricType, AverageUtilization: i32(80)},
					},
				},
				{
					Type: autoscalingv2.PodsMetricSourceType,
					Pods: &autoscalingv2.PodsMetricSource{
						Metric: autoscalingv2.MetricIdentifier{Name: "packets-per-second"},
						Target: autoscalingv2.MetricTarget{Type: autoscalingv2.AverageValueMetricType, AverageValue: qty("1k")},
					},
				},
				{
					Type: autoscalingv2.ObjectMetricSourceType,
					Object: &autoscalingv2.ObjectMetricSource{
						Metric: autoscalingv2.MetricIdentifier{Name: "requests-per-second"},
						Target: autoscalingv2.MetricTarget{Type: autoscalingv2.ValueMetricType, Value: qty("100")},
					},
				},
				{
					// Both value and averageValue set -> two series, like v1.9.7.
					Type: autoscalingv2.ExternalMetricSourceType,
					External: &autoscalingv2.ExternalMetricSource{
						Metric: autoscalingv2.MetricIdentifier{Name: "queue-length"},
						Target: autoscalingv2.MetricTarget{Value: qty("30"), AverageValue: qty("3")},
					},
				},
				{
					// ContainerResource has no v1.9.7 equivalent and no container label
					// to disambiguate; it must emit nothing (asserted below).
					Type: autoscalingv2.ContainerResourceMetricSourceType,
					ContainerResource: &autoscalingv2.ContainerResourceMetricSource{
						Name:      corev1.ResourceMemory,
						Container: "app",
						Target:    autoscalingv2.MetricTarget{Type: autoscalingv2.AverageValueMetricType, AverageValue: qty("256Mi")},
					},
				},
				{
					// Malformed: type set but source struct nil -> skipped, no panic.
					Type: autoscalingv2.ResourceMetricSourceType,
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := writeMetrics(&buf, []*autoscalingv2.HorizontalPodAutoscaler{hpa}); err != nil {
		t.Fatalf("writeMetrics: %v", err)
	}
	out := buf.String()

	want := []string{
		"# HELP kube_hpa_spec_target_metric The metric specifications used by this autoscaler",
		"# TYPE kube_hpa_spec_target_metric gauge",
		`kube_hpa_spec_target_metric{namespace="ns",hpa="h",metric_name="cpu",metric_target_type="utilization"} 80`,
		`kube_hpa_spec_target_metric{namespace="ns",hpa="h",metric_name="packets-per-second",metric_target_type="average"} 1000`,
		`kube_hpa_spec_target_metric{namespace="ns",hpa="h",metric_name="requests-per-second",metric_target_type="value"} 100`,
		`kube_hpa_spec_target_metric{namespace="ns",hpa="h",metric_name="queue-length",metric_target_type="value"} 30`,
		`kube_hpa_spec_target_metric{namespace="ns",hpa="h",metric_name="queue-length",metric_target_type="average"} 3`,
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("output missing line:\n%s\n--- full output ---\n%s", w, out)
		}
	}

	// ContainerResource is not emitted (no v1.9.7 equivalent; the metric has no
	// container label to disambiguate), so the memory ContainerResource above must
	// produce no series.
	if strings.Contains(out, `metric_name="memory"`) {
		t.Errorf("ContainerResource must not emit a target metric series:\n%s", out)
	}
}

// TestWriteTargetMetricFractionalSkipped guards parity with kube-state-metrics
// v1.9.7: a target Quantity that is not an exact integer (AsInt64 ok=false, e.g.
// "1500m") must be dropped, not emitted as a phantom 0-valued series. A Resource
// averageValue that IS an exact integer must still be emitted.
func TestWriteTargetMetricFractionalSkipped(t *testing.T) {
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "h"},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			MaxReplicas: 5,
			Metrics: []autoscalingv2.MetricSpec{
				{
					// Fractional averageValue -> AsInt64 ok=false -> no series.
					Type: autoscalingv2.PodsMetricSourceType,
					Pods: &autoscalingv2.PodsMetricSource{
						Metric: autoscalingv2.MetricIdentifier{Name: "frac"},
						Target: autoscalingv2.MetricTarget{Type: autoscalingv2.AverageValueMetricType, AverageValue: qty("1500m")},
					},
				},
				{
					// Fractional value (0.5) -> AsInt64 ok=false -> no series.
					Type: autoscalingv2.ObjectMetricSourceType,
					Object: &autoscalingv2.ObjectMetricSource{
						Metric: autoscalingv2.MetricIdentifier{Name: "half"},
						Target: autoscalingv2.MetricTarget{Type: autoscalingv2.ValueMetricType, Value: qty("0.5")},
					},
				},
				{
					// Integer Resource averageValue -> emitted (covers the Resource
					// + averageValue branch).
					Type: autoscalingv2.ResourceMetricSourceType,
					Resource: &autoscalingv2.ResourceMetricSource{
						Name:   corev1.ResourceMemory,
						Target: autoscalingv2.MetricTarget{Type: autoscalingv2.AverageValueMetricType, AverageValue: qty("2Gi")},
					},
				},
			},
		},
	}

	var buf bytes.Buffer
	if err := writeMetrics(&buf, []*autoscalingv2.HorizontalPodAutoscaler{hpa}); err != nil {
		t.Fatalf("writeMetrics: %v", err)
	}
	out := buf.String()

	for _, frac := range []string{`metric_name="frac"`, `metric_name="half"`} {
		if strings.Contains(out, frac) {
			t.Errorf("fractional Quantity target must be skipped (matching v1.9.7), got %s:\n%s", frac, out)
		}
	}
	if !strings.Contains(out, "# TYPE kube_hpa_spec_target_metric gauge") {
		t.Errorf("family HELP/TYPE line missing:\n%s", out)
	}
	if !strings.Contains(out, `kube_hpa_spec_target_metric{namespace="ns",hpa="h",metric_name="memory",metric_target_type="average"} 2147483648`) {
		t.Errorf("integer Resource averageValue (2Gi) should be emitted:\n%s", out)
	}
}
