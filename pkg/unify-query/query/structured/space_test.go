// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package structured

import (
	"context"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query"
)

func TestSpaceFilter_NewTsDBs(t *testing.T) {
	metadata.InitMetadata()
	ctx := metadata.InitHashID(context.Background())
	mock.Init()

	testCases := map[string]struct {
		tableID      TableID
		fieldName    string
		isRegexp     bool
		allCondition AllConditions

		isSkipSpace bool
		isSkipField bool
		isSkipK8s   bool

		expected string
	}{
		"test_1": {
			fieldName: "kube_node_info",
			tableID:   "",
			expected:  `[{"table_id":"result_table.influxdb","field":["kube_pod_info","kube_node_info","kube_node_status_condition","kubelet_cluster_request_total","merltrics_rest_request_status_200_count","merltrics_rest_request_status_500_count"],"measurement_type":"bk_split_measurement","data_label":"influxdb","storage_id":"2","cluster_name":"default","db":"result_table","measurement":"influxdb","metric_name":"kube_node_info","expand_metric_names":["kube_node_info"],"time_field":{},"need_add_time":false,"storage_type":"influxdb"}]`,
		},
		"test_2_regex": {
			fieldName: "kubelet_.+",
			isRegexp:  true,
			expected:  `[{"table_id":"result_table.influxdb","field":["kube_pod_info","kube_node_info","kube_node_status_condition","kubelet_cluster_request_total","merltrics_rest_request_status_200_count","merltrics_rest_request_status_500_count"],"measurement_type":"bk_split_measurement","data_label":"influxdb","storage_id":"2","cluster_name":"default","db":"result_table","measurement":"influxdb","metric_name":"kubelet_.+","expand_metric_names":["kubelet_cluster_request_total"],"time_field":{},"need_add_time":false,"storage_type":"influxdb"},{"table_id":"result_table.vm","field":["container_cpu_usage_seconds_total","kube_pod_info","node_with_pod_relation","node_with_system_relation","deployment_with_replicaset_relation","pod_with_replicaset_relation","apm_service_instance_with_pod_relation","apm_service_instance_with_system_relation","container_info_relation","host_info_relation","kubelet_info"],"measurement_type":"bk_split_measurement","data_label":"kubelet_info","storage_id":"2","db":"other","measurement":"kubelet_info","vm_rt":"2_bcs_prom_computation_result_table","metric_name":"kubelet_.+","expand_metric_names":["kubelet_info"],"time_field":{},"need_add_time":false,"storage_type":"victoria_metrics"}]`,
		},
		"test_3_regex": {
			fieldName: "container_.+",
			isRegexp:  true,
			expected:  `[{"table_id":"result_table.vm","field":["container_cpu_usage_seconds_total","kube_pod_info","node_with_pod_relation","node_with_system_relation","deployment_with_replicaset_relation","pod_with_replicaset_relation","apm_service_instance_with_pod_relation","apm_service_instance_with_system_relation","container_info_relation","host_info_relation","kubelet_info"],"measurement_type":"bk_split_measurement","data_label":"vm","storage_id":"2","vm_rt":"2_bcs_prom_computation_result_table","metric_name":"container_.+","expand_metric_names":["container_cpu_usage_seconds_total","container_info_relation"],"time_field":{},"need_add_time":false,"storage_type":"victoria_metrics"}]`,
		},
		"test_4_incomplete_tableid_from_datalabel": {
			fieldName: "kube_pod_info",
			tableID:   "influxdb",
			expected:  `[{"table_id":"result_table.influxdb","field":["kube_pod_info","kube_node_info","kube_node_status_condition","kubelet_cluster_request_total","merltrics_rest_request_status_200_count","merltrics_rest_request_status_500_count"],"measurement_type":"bk_split_measurement","data_label":"influxdb","storage_id":"2","cluster_name":"default","db":"result_table","measurement":"influxdb","metric_name":"kube_pod_info","expand_metric_names":["kube_pod_info"],"time_field":{},"need_add_time":false,"storage_type":"influxdb"},{"table_id":"result_table.vm","field":["container_cpu_usage_seconds_total","kube_pod_info","node_with_pod_relation","node_with_system_relation","deployment_with_replicaset_relation","pod_with_replicaset_relation","apm_service_instance_with_pod_relation","apm_service_instance_with_system_relation","container_info_relation","host_info_relation","kubelet_info"],"measurement_type":"bk_split_measurement","data_label":"vm","storage_id":"2","vm_rt":"2_bcs_prom_computation_result_table","metric_name":"kube_pod_info","expand_metric_names":["kube_pod_info"],"time_field":{},"need_add_time":false,"storage_type":"victoria_metrics"}]`,
		},
	}

	for name, c := range testCases {
		t.Run(name, func(t *testing.T) {
			ctx = metadata.InitHashID(ctx)
			influxdb.MockSpaceRouter(ctx)

			sf, err := NewSpaceFilter(ctx, &TsDBOption{
				SpaceUid: influxdb.SpaceUid,
			})
			assert.NoError(t, err)

			tsdb, err := sf.DataList(&TsDBOption{
				IsSkipSpace:   c.isSkipSpace,
				IsSkipField:   c.isSkipField,
				IsSkipK8s:     c.isSkipK8s,
				TableID:       c.tableID,
				FieldName:     c.fieldName,
				IsRegexp:      c.isRegexp,
				AllConditions: c.allCondition,
			})

			actual := toJson(tsdb)
			assert.Equal(t, c.expected, actual)
		})
	}
}

func toJson(q []*query.TsDBV2) string {
	sort.SliceStable(q, func(i, j int) bool {
		return q[i].TableID < q[j].TableID
	})

	s, _ := json.Marshal(q)
	return string(s)
}
