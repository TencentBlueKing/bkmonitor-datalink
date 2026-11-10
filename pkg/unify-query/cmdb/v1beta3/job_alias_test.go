// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package v1beta3

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
)

func TestBuildTimeGraphRequestJobAlias(t *testing.T) {
	// 使用服务内置 schema 验证真实主键及资源推断顺序。
	model := &Model{schemaProvider: NewStaticSchemaProvider()}
	tests := []struct {
		name       string
		sourceType cmdb.Resource
		matcher    cmdb.Matcher
		wantType   ResourceType
		wantInfo   map[string]string
	}{
		{
			name:     "省略源类型时识别 Job 别名",
			matcher:  cmdb.Matcher{"bcs_cluster_id": "cluster", "namespace": "default", "job_name": "backup-1"},
			wantType: "job",
			wantInfo: map[string]string{"bcs_cluster_id": "cluster", "namespace": "default", "job": "backup-1"},
		},
		{
			name:       "显式 Job 类型与采集标签",
			sourceType: "job",
			matcher:    cmdb.Matcher{"bcs_cluster_id": "cluster", "namespace": "default", "job_name": "backup-1", "job": "kube-state-metrics"},
			wantType:   "job",
			wantInfo:   map[string]string{"bcs_cluster_id": "cluster", "namespace": "default", "job": "backup-1"},
		},
		{
			name:     "Pod 附加 Job 标签仍识别为 Pod",
			matcher:  cmdb.Matcher{"bcs_cluster_id": "cluster", "namespace": "default", "pod_name": "backup-1-pod", "job_name": "backup-1"},
			wantType: "pod",
			wantInfo: map[string]string{"bcs_cluster_id": "cluster", "namespace": "default", "pod": "backup-1-pod"},
		},
		{
			name:     "Container 附加 Job 标签仍识别为 Container",
			matcher:  cmdb.Matcher{"bcs_cluster_id": "cluster", "namespace": "default", "pod_name": "backup-1-pod", "container_name": "backup", "job_name": "backup-1"},
			wantType: "container",
			wantInfo: map[string]string{"bcs_cluster_id": "cluster", "namespace": "default", "pod": "backup-1-pod", "container": "backup"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, _, err := model.buildTimeGraphRequest(context.Background(), "bkcc__2", "", tc.sourceType, tc.matcher, nil, false, nil)
			require.NoError(t, err)
			require.Equal(t, tc.wantType, req.SourceType)
			require.Equal(t, tc.wantInfo, req.SourceInfo)
		})
	}
}
