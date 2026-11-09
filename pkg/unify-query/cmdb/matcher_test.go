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
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMatcherRename(t *testing.T) {
	for name, c := range map[string]struct {
		in, want Matcher
	}{
		"job_name names the job": {
			in:   Matcher{"bcs_cluster_id": "c", "namespace": "ns", "job_name": "migrate-1"},
			want: Matcher{"bcs_cluster_id": "c", "namespace": "ns", "job": "migrate-1"},
		},
		"job_name wins over the scrape job label": {
			in:   Matcher{"namespace": "ns", "job": "kube-state-metrics", "job_name": "migrate-1"},
			want: Matcher{"namespace": "ns", "job": "migrate-1"},
		},
		"pod_name wins over pod": {
			in:   Matcher{"pod": "other", "pod_name": "p-1"},
			want: Matcher{"pod": "p-1"},
		},
		"an empty alias leaves the key as it is": {
			in:   Matcher{"job": "j", "job_name": ""},
			want: Matcher{"job": "j"},
		},
		"plain keys stay and empty values are dropped": {
			in:   Matcher{"job": "j", "container": "", "node": "n"},
			want: Matcher{"job": "j", "node": "n"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			// Map iteration order varies, so the result must not depend on it.
			for range 50 {
				require.Equal(t, c.want, c.in.Rename())
			}
		})
	}
}
