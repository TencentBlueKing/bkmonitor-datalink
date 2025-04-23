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

	Items []*DataID `json:"items"`
}
