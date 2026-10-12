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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// Every physical query whose answer may have been cut is projected, one
// entry each, with its source and dimension; the others are not.
func TestEveryTruncatedQueryIsProjected(t *testing.T) {
	cut := func(source, dimension string) execution.ProviderRouteFacts {
		return execution.ProviderRouteFacts{Truncation: &execution.ProviderTruncationFact{Kind: execution.TruncationTermsCut,
			Dimension: dimension, Cap: 10000, SourceSemantics: source}}
	}
	completion := execution.QueryExecutionCompletion{PhysicalQueries: []execution.PhysicalQueryCompletion{
		{RouteFacts: cut("custom/event", "host")}, {}, {RouteFacts: cut("bk_log_search/log", "pod")},
	}}
	facts := providerTruncationFacts(completion)
	if len(facts) != 2 || facts[0].Source != "custom/event" || facts[0].Dimension != "host" || facts[1].Source != "bk_log_search/log" {
		t.Fatalf("projected %+v, want the two cut queries with their source and dimension", facts)
	}
}
