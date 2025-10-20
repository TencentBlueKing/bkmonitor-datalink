// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta1

import (
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/utils"
)

const (
	Version = "v1beta1"
)

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type DataID struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ObjectMeta `json:"metadata,omitempty"`
	// +optional
	Spec DataIDSpec `json:"spec,omitempty"`
}

type DataIDSpec struct {
	DataID          int               `json:"dataID,omitempty"`
	MonitorResource MonitorResource   `json:"monitorResource,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`

	// 以下字段已弃用
	Report           Report            `json:"report,omitempty"`
	MetricReplace    map[string]string `json:"metricReplace,omitempty"`
	DimensionReplace map[string]string `json:"dimensionReplace,omitempty"`
}

type MonitorResource struct {
	// 资源所属命名空间
	NameSpace string `json:"namespace,omitempty"`
	// 资源所属类型 ServiceMonitor/PodMonitor/Probe
	Kind string `json:"kind,omitempty"`
	// 资源名称
	Name string `json:"name,omitempty"`
}

// MatchSplitNamespace namespace 支持使用【|】分割
func (mr *MonitorResource) MatchSplitNamespace(namespace string) bool {
	for _, ns := range strings.Split(mr.NameSpace, "|") {
		if strings.TrimSpace(ns) == namespace {
			return true
		}
	}
	return false
}

// MatchSplitKind kind 支持使用【|】分割
func (mr *MonitorResource) MatchSplitKind(kind string) bool {
	for _, s := range strings.Split(mr.Kind, "|") {
		if utils.LowerEq(strings.TrimSpace(s), kind) {
			return true
		}
	}
	return false
}

// MatchSplitName name 支持使用【|】分割
func (mr *MonitorResource) MatchSplitName(name string) bool {
	for _, s := range strings.Split(mr.Name, "|") {
		if strings.TrimSpace(s) == name {
			return true
		}
	}
	return false
}

type Report struct {
	MaxMetric   int `json:"maxMetric,omitempty"`
	MaxSizeByte int `json:"maxSizeByte,omitempty"`
}

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type DataIDList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []DataID `json:"items"`
}

// Duration is a valid time duration that can be parsed by Prometheus model.ParseDuration() function.
// Supported units: y, w, d, h, m, s, ms
// Examples: `30s`, `1m`, `1h20m15s`, `15d`
// +kubebuilder:validation:Pattern:="^(0|(([0-9]+)y)?(([0-9]+)w)?(([0-9]+)d)?(([0-9]+)h)?(([0-9]+)m)?(([0-9]+)s)?(([0-9]+)ms)?)$"
type Duration string

// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type QCloudMonitorList struct {
	metav1.TypeMeta `json:",inline"`
	// +optional
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []QCloudMonitor `json:"items"`
}

// +genclient
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
type QCloudMonitor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec QCloudMonitorSpec `json:"spec,omitempty"`
}

type QCloudMonitorSpec struct {
	// +kubebuilder:validation:Minimum=1
	DataID int `json:"dataID,omitempty"`

	// Interval is the frequency at which the task scrapes metrics.
	Interval Duration `json:"interval,omitempty"`

	// Timeout is the timeout at which the task scrapes metrics.
	Timeout Duration `json:"timeout,omitempty"`

	// ExtendLabels are additional labels that will be appended to each metric before reporting.
	ExtendLabels map[string]string `json:"extendLabels,omitempty"`

	// MetricRelabelings defines the standard Prometheus metric relabeling rules.
	MetricRelabelings []RelabelConfig `json:"metricRelabelings,omitempty"`

	// Exporter represents the actual workload running the exporter deployment instance.
	Exporter QCloudMonitorExporter `json:"exporter,omitempty"`

	// Config contains the exporter's collection configuration.
	Config QCloudMonitorConfig `json:"config,omitempty"`
}

type QCloudMonitorExporter struct {
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// +kubebuilder:validation:MinLength=1
	Image string `json:"image,omitempty"`

	// +kubebuilder:validation:Enum="";Always;Never;IfNotPresent
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// See http://kubernetes.io/docs/user-guide/images#specifying-imagepullsecrets-on-a-pod
	ImagePullSecrets []corev1.LocalObjectReference `json:"imagePullSecrets,omitempty"`
}

// LabelName is a valid Prometheus label name which may only contain ASCII
// letters, numbers, as well as underscores.
//
// +kubebuilder:validation:Pattern:="^[a-zA-Z_][a-zA-Z0-9_]*$"
type LabelName string

// RelabelConfig allows dynamic rewriting of the label set for targets, alerts,
// scraped samples and remote write samples.
//
// More info: https://prometheus.io/docs/prometheus/latest/configuration/configuration/#relabel_config
//
// +k8s:openapi-gen=true
type RelabelConfig struct {
	// The source labels select values from existing labels. Their content is
	// concatenated using the configured Separator and matched against the
	// configured regular expression.
	//
	// +optional
	SourceLabels []LabelName `json:"sourceLabels,omitempty"`

	// Separator is the string between concatenated SourceLabels.
	Separator *string `json:"separator,omitempty"`

	// Label to which the resulting string is written in a replacement.
	//
	// It is mandatory for `Replace`, `HashMod`, `Lowercase`, `Uppercase`,
	// `KeepEqual` and `DropEqual` actions.
	//
	// Regex capture groups are available.
	TargetLabel string `json:"targetLabel,omitempty"`

	// Regular expression against which the extracted value is matched.
	Regex string `json:"regex,omitempty"`

	// Modulus to take of the hash of the source label values.
	//
	// Only applicable when the action is `HashMod`.
	Modulus uint64 `json:"modulus,omitempty"`

	// Replacement value against which a Replace action is performed if the
	// regular expression matches.
	//
	// Regex capture groups are available.
	//
	//+optional
	Replacement *string `json:"replacement,omitempty"`

	// Action to perform based on the regex matching.
	//
	// `Uppercase` and `Lowercase` actions require Prometheus >= v2.36.0.
	// `DropEqual` and `KeepEqual` actions require Prometheus >= v2.41.0.
	//
	// Default: "Replace"
	//
	// +kubebuilder:validation:Enum=replace;Replace;keep;Keep;drop;Drop;hashmod;HashMod;labelmap;LabelMap;labeldrop;LabelDrop;labelkeep;LabelKeep;lowercase;Lowercase;uppercase;Uppercase;keepequal;KeepEqual;dropequal;DropEqual
	// +kubebuilder:default=replace
	Action string `json:"action,omitempty"`
}

type QCloudMonitorConfig struct {
	// EnableExporterMetrics indicates whether to enable exporter metrics.
	// +optional
	EnableExporterMetrics *bool `json:"enableExporterMetrics,omitempty"`

	// MaxRequests defines the maximum concurrency for scraping /metrics.
	// Default to 0 (no limit).
	//
	// +optional
	MaxRequests *int `json:"maxRequests,omitempty"`

	// LogLevel specifies the logging level.
	//
	// +kubebuilder:default=info
	// +kubebuilder:validation:Enum=debug;info;warn;error
	LogLevel string `json:"logLevel,omitempty"`

	// FileContent contains the actual configuration text for the exporter.
	// This content will be generated into a ConfigMap and mounted to the exporter instance.
	//
	// +kubebuilder:validation:MinLength=1
	FileContent string `json:"fileContent"`
}
