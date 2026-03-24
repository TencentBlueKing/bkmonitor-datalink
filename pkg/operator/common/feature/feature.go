// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package feature

import (
	"strconv"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/utils"
)

const (
	// labels features
	keyCommonResource = "isCommon"
	keySystemResource = "isSystem"
	keyBkEnv          = "bk_env"
	keyDataIDUsage    = "usage"

	// annotations features
	keyForwardLocalhost     = "forwardLocalhost"
	keyNormalizeMetricName  = "normalizeMetricName"
	keyAntiAffinity         = "antiAffinity"
	keyRelabelRule          = "relabelRule"
	keyRelabelIndex         = "relabelIndex"
	keyMonitorMatchSelector = "monitorMatchSelector"
	keyMonitorDropSelector  = "monitorDropSelector"
	keyLabelJoinMatcher     = "labelJoinMatcher"

	// KeyScheduledDataID Monitor 资源直接指定 DataID
	// 优先级高于 DataID Resource 自身匹配规则
	KeyScheduledDataID = "scheduledDataID"

	// KeyExtendLabels Monitor 资源扩展 Labels
	// 优先级高于 DataID Resource 自身 Labels
	KeyExtendLabels = "extendLabels"
)

func isMapKeyExists(m map[string]string, key string) bool {
	if value, ok := m[key]; ok {
		if value == "true" {
			return true
		}
	}
	return false
}

const (
	LabelJoinMatcherKindPod  = "Pod"
	LabelJoinMatcherKindNode = "Node"
)

type LabelJoinMatcherSpec struct {
	Kind        string
	Annotations []string
	Labels      []string
}

// parseLabelJoinMatcher 解析 labeljoin 规则
// Kind://[label:custom_label|annotation:custom_annotation,...]
func parseLabelJoinMatcher(s string) *LabelJoinMatcherSpec {
	const (
		annotationPrefix = "annotation:"
		labelPrefix      = "label:"
	)

	var kind string
	switch {
	case strings.HasPrefix(s, "Pod://"):
		s = s[len("Pod://"):]
		kind = LabelJoinMatcherKindPod

	case strings.HasPrefix(s, "Node://"):
		s = s[len("Node://"):]
		kind = LabelJoinMatcherKindNode

	default:
		return nil
	}

	var annotations []string
	var labels []string
	parts := strings.Split(s, ",")
	for _, part := range parts {
		k := strings.TrimSpace(part)
		switch {
		case strings.HasPrefix(k, annotationPrefix):
			annotations = append(annotations, strings.TrimSpace(k[len(annotationPrefix):]))
		case strings.HasPrefix(k, labelPrefix):
			labels = append(labels, strings.TrimSpace(k[len(labelPrefix):]))
		}
	}

	return &LabelJoinMatcherSpec{
		Kind:        kind,
		Annotations: annotations,
		Labels:      labels,
	}
}

// IfCommonResource 检查 DataID 是否为 common 类型
func IfCommonResource(m map[string]string) bool {
	return isMapKeyExists(m, keyCommonResource)
}

// IfSystemResource 检查 DataID 是否为 system 类型
func IfSystemResource(m map[string]string) bool {
	return isMapKeyExists(m, keySystemResource)
}

// IfForwardLocalhost 检查采集端点是否需要重定向到 localhost
func IfForwardLocalhost(m map[string]string) bool {
	return isMapKeyExists(m, keyForwardLocalhost)
}

// IfNormalizeMetricName 检查是否需要标准化指标名
func IfNormalizeMetricName(m map[string]string) bool {
	return isMapKeyExists(m, keyNormalizeMetricName)
}

// IfAntiAffinity 检查调度时是否需要反节点亲和
func IfAntiAffinity(m map[string]string) bool {
	return isMapKeyExists(m, keyAntiAffinity)
}

func BkEnv(m map[string]string) string {
	return m[keyBkEnv]
}

func DataIDUsage(m map[string]string) string {
	return m[keyDataIDUsage]
}

func RelabelRule(m map[string]string) string {
	return m[keyRelabelRule]
}

func RelabelIndex(m map[string]string) string {
	return m[keyRelabelIndex]
}

func MonitorMatchSelector(m map[string]string) map[string]string {
	return utils.SelectorToMap(m[keyMonitorMatchSelector])
}

func MonitorDropSelector(m map[string]string) map[string]string {
	return utils.SelectorToMap(m[keyMonitorDropSelector])
}

func LabelJoinMatcher(m map[string]string) *LabelJoinMatcherSpec {
	return parseLabelJoinMatcher(m[keyLabelJoinMatcher])
}

func ExtendLabels(m map[string]string) map[string]string {
	return utils.SelectorToMap(m[KeyExtendLabels])
}

func ScheduledDataID(m map[string]string) int {
	v, ok := m[KeyScheduledDataID]
	if !ok {
		return 0
	}

	i, err := strconv.Atoi(v)
	if err != nil {
		return 0
	}
	return i
}
