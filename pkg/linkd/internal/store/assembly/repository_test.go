// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package assembly

import "testing"

func TestElasticsearchConnectionBudgetBounds(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		requested int
		want      int
	}{
		{requested: 1, want: minElasticsearchConnectionsPerHost},
		{requested: 17, want: 17},
		{requested: maxElasticsearchConnectionsPerHost + 1, want: maxElasticsearchConnectionsPerHost},
	} {
		if got := elasticsearchConnectionBudget(test.requested); got != test.want {
			t.Fatalf("elasticsearchConnectionBudget(%d)=%d, want %d", test.requested, got, test.want)
		}
	}
}
