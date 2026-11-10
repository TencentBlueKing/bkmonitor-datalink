// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdb

import (
	"maps"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMatcherRename(t *testing.T) {
	tests := []struct {
		name    string
		matcher Matcher
		want    Matcher
	}{
		{
			name:    "Job 名称别名",
			matcher: Matcher{"bcs_cluster_id": "cluster", "namespace": "default", "job_name": "backup-1"},
			want:    Matcher{"bcs_cluster_id": "cluster", "namespace": "default", "job": "backup-1"},
		},
		{
			// job 可以表示采集任务，显式 Job 名称应具有稳定的优先级。
			name:    "Job 名称优先于采集任务标签",
			matcher: Matcher{"job_name": "backup-1", "job": "kube-state-metrics"},
			want:    Matcher{"job": "backup-1"},
		},
		{
			name:    "相同 Job 字段",
			matcher: Matcher{"job_name": "backup-1", "job": "backup-1"},
			want:    Matcher{"job": "backup-1"},
		},
		{
			name:    "标准 Job 字段保持",
			matcher: Matcher{"job": "backup-1"},
			want:    Matcher{"job": "backup-1"},
		},
		{
			name:    "空 Job 别名保留已有字段",
			matcher: Matcher{"job_name": "", "job": "backup-1"},
			want:    Matcher{"job": "backup-1"},
		},
		{
			name:    "空值继续忽略",
			matcher: Matcher{"job_name": "", "namespace": ""},
			want:    Matcher{},
		},
		{
			name:    "已有 Pod 与 Container 别名保持",
			matcher: Matcher{"pod_name": "backup-1-pod", "container_name": "backup", "region": "east"},
			want:    Matcher{"pod": "backup-1-pod", "container": "backup", "region": "east"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			original := maps.Clone(tc.matcher)
			require.Equal(t, tc.want, tc.matcher.Rename())
			require.Equal(t, original, tc.matcher, "字段归一化应保留调用方输入")
		})
	}
}
