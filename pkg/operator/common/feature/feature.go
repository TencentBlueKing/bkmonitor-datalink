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
	"strings"
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
	keyCadvisorExtraInfo    = "cadvisorExtraInfo"
)

func isMapKeyExists(m map[string]string, key string) bool {
	if value, ok := m[key]; ok {
		if value == "true" {
			return true
		}
	}
	return false
}

func parseSelector(s string) map[string]string {
	selector := make(map[string]string)
	parts := strings.Split(s, ",")
	for _, part := range parts {
		kv := strings.Split(strings.TrimSpace(part), "=")
		if len(kv) != 2 {
			continue
		}
		selector[kv[0]] = kv[1]
	}
	return selector
}

func parseCadvisorExtraInfo(s string) ([]string, []string) {
	const (
		annotationPrefix = "annotation:"
		labelPrefix      = "label:"
	)

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

	return annotations, labels
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
	return parseSelector(m[keyMonitorMatchSelector])
}

func MonitorDropSelector(m map[string]string) map[string]string {
	return parseSelector(m[keyMonitorDropSelector])
}

func CadvisorExtraInfo(m map[string]string) ([]string, []string) {
	return parseCadvisorExtraInfo(m[keyCadvisorExtraInfo])
}
