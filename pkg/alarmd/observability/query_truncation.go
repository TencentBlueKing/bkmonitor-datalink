// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package observability

// QueryTruncationFacts is one physical query whose answer the query service
// may have cut without marking it: a terms level of its Elasticsearch
// aggregation held exactly the service's bucket cap, and groups past the cap
// are dropped silently. The series it returned are read as they are; the
// groups it may have dropped are not known absent. Source is the query's
// source semantics, one of TruncationSources; Dimension the group-by
// dimension whose level held the cap, for the log line only.
type QueryTruncationFacts struct {
	Source    string
	Dimension string
}

// TruncationSources are the sources answered through Elasticsearch terms
// aggregations, the only ones such a cut is suspected for: a closed list, and
// the label set of the counter.
var TruncationSources = []string{"custom/event", "bk_log_search/log", "bk_monitor/log", "bk_log_search/time_series"}

func normalizeQueryTruncation(facts []QueryTruncationFacts) []QueryTruncationFacts {
	var kept []QueryTruncationFacts
	for _, item := range facts {
		if inList(item.Source, TruncationSources) {
			kept = append(kept, item)
		}
	}
	return kept
}

// QueryRangeFacts is one primary physical query's range as it was sent: its
// digest, how long a range it asked the provider for and how long a range it
// accepts, in seconds. An event count asks from its lead earlier than it
// accepts; any other query asks what it accepts.
type QueryRangeFacts struct {
	Digest          string
	AskedSeconds    int64
	AcceptedSeconds int64
}
