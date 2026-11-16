// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package relation

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// 期望是逐条核对的归属端点（子 → 父）与指标写方核对出的指标名，不取自被测定义本身。
func TestDefaultRelationBelongsToChildToParent(t *testing.T) {
	belongs := map[string][2]string{
		"container_with_pod":                    {"container", "pod"},
		"pod_with_replicaset":                   {"pod", "replicaset"},
		"pod_with_statefulset":                  {"pod", "statefulset"},
		"pod_with_service":                      {"pod", "service"},
		"k8s_address_with_service":              {"k8s_address", "service"},
		"host_with_module":                      {"host", "module"},
		"module_with_set":                       {"module", "set"},
		"deployment_with_replicaset":            {"replicaset", "deployment"},
		"daemonset_with_pod":                    {"pod", "daemonset"},
		"job_with_pod":                          {"pod", "job"},
		"node_with_pod":                         {"pod", "node"},
		"business_with_set":                     {"set", "business"},
		"biz_with_set":                          {"set", "biz"},
		"apm_service_with_apm_service_instance": {"apm_service_instance", "apm_service"},
		"apm_service_instance_with_pod":         {"pod", "apm_service_instance"},
		"apm_service_instance_with_system":      {"system", "apm_service_instance"},
		"ingress_with_service":                  {"service", "ingress"},
	}
	plain := map[string]bool{
		"node_with_system": true, "host_with_system": true, "domain_with_service": true,
		"datasource_with_pod": true, "datasource_with_node": true, "bklogconfig_with_datasource": true,
		"app_version_with_host": true, "app_version_with_container": true, "app_version_with_git_commit": true,
		"app_version_with_p4_changelist": true, "app_version_with_svn_revision": true,
	}
	metrics := map[string]string{
		"pod_to_pod": "pod_to_pod_flow", "pod_to_system": "pod_to_system_flow", "system_to_pod": "system_to_pod_flow",
		"system_to_system": "system_to_system_flow_count", "service_to_service": "service_to_service_flow",
		"apm_service_to_apm_service": "apm_service_to_apm_service_flow_count",
	}
	seen := map[string]bool{}
	for _, definition := range DefaultRelationDefinitions() {
		seen[definition.Name] = true
		switch {
		case definition.Category == string(RelationCategoryDynamic):
			require.False(t, definition.IsBelongsTo, definition.Name)
			require.Equal(t, metrics[definition.Name], definition.Labels["metric_name"], definition.Name)
		case plain[definition.Name]:
			require.False(t, definition.IsBelongsTo, definition.Name)
		default:
			pair, ok := belongs[definition.Name]
			require.True(t, ok, "unexpected static relation %s", definition.Name)
			require.True(t, definition.IsBelongsTo, definition.Name)
			require.Equal(t, pair[0], definition.FromResource, "%s: from is the child", definition.Name)
			require.Equal(t, pair[1], definition.ToResource, "%s: to is the parent", definition.Name)
		}
		if definition.Category != string(RelationCategoryDynamic) {
			// 名字与指标名按两端类型的字母序生成，交换 from/to 不改变它们。
			require.Equal(t, definition.Name+"_relation", definition.GetRelationName(), definition.Name)
		}
	}
	require.Len(t, seen, len(belongs)+len(plain)+len(metrics))
}
