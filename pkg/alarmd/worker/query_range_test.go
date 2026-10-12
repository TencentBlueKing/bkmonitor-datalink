// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"reflect"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// Every primary physical query's range is projected, one entry each with its
// digest - two primaries of different leads are two entries - and a query
// that serves no primary requirement, or names no range, is not.
func TestEveryPrimaryQueryRangeIsProjected(t *testing.T) {
	completion := execution.QueryExecutionCompletion{PhysicalQueries: []execution.PhysicalQueryCompletion{
		{PhysicalQuery: "query-a", Range: &execution.PhysicalQueryRange{Primary: true, AskedSeconds: 600, AcceptedSeconds: 60}},
		{PhysicalQuery: "query-b", Range: &execution.PhysicalQueryRange{AskedSeconds: 120, AcceptedSeconds: 120}},
		{PhysicalQuery: "query-c"},
		{PhysicalQuery: "query-d", Range: &execution.PhysicalQueryRange{Primary: true, AskedSeconds: 300, AcceptedSeconds: 300}},
	}}
	want := []observability.QueryRangeFacts{
		{Digest: "query-a", AskedSeconds: 600, AcceptedSeconds: 60},
		{Digest: "query-d", AskedSeconds: 300, AcceptedSeconds: 300},
	}
	if got := providerRangeFacts(completion); !reflect.DeepEqual(got, want) {
		t.Fatalf("projected %+v, want the two primary queries' ranges %+v", got, want)
	}
}
