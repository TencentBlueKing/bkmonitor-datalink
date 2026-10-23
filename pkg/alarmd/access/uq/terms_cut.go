// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"slices"
	"sort"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// esTermsCap is the query service's bucket count for one terms level of an
// Elasticsearch aggregation: EsMaxSize, 1e4 by default (unify-query
// service/tsdb/hook.go, applied at tsdb/elasticsearch/format.go). Every
// group-by dimension is one level, nested in reverse request order, and an
// inner level's cap applies per parent bucket. The groups past it are not
// returned and nothing marks the answer. A deployment that changed EsMaxSize
// is not seen here.
const esTermsCap = 10000

// termsCutCounter keeps each series' group values of an answer that may have
// been cut, to tell afterwards whether a level held exactly the cap.
type termsCutCounter struct {
	source string
	series []map[string]string
}

// newTermsCutCounter is a counter for an answer from an Elasticsearch-family
// source grouped by at least one dimension, and nil for any other.
func newTermsCutCounter(spec execution.PhysicalQuerySpec) *termsCutCounter {
	semantics := spec.PlanFacts.SourceSemantics
	// The sources answered through Elasticsearch terms aggregations; another
	// storage has no such cap, and an answer from one that happens to hold
	// that many values is not marked.
	if len(semantics) != 1 || !slices.Contains(observability.TruncationSources, semantics[0]) ||
		len(spec.PlanFacts.Normalization.DatasetContract.IdentityFields) == 0 {
		return nil
	}
	return &termsCutCounter{source: semantics[0]}
}

func (counter *termsCutCounter) add(series responseSeries) {
	if counter == nil {
		return
	}
	values := make(map[string]string, len(series.GroupKeys))
	for index, key := range series.GroupKeys {
		if index < len(series.GroupValues) {
			values[key] = string(series.GroupValues[index])
		}
	}
	counter.series = append(counter.series, values)
}

// suspected is the dimension whose level was cut, as the answer reads: for
// some group of series agreeing on every other dimension, exactly the cap of
// distinct values. That group is finer than the query service's own parent
// bucket - the outer dimensions only - so the parent held at least the cap,
// and at most it: it was at the cap. A parent that holds exactly the cap
// uncut reads the same, which is why this is a suspicion; a cut whose finer
// groups all fall below the cap is not seen. Fewer series than the cap in
// the whole answer cannot hold a level at it.
func (counter *termsCutCounter) suspected() (string, bool) {
	if counter == nil || len(counter.series) < esTermsCap {
		return "", false
	}
	dimensionSet := map[string]struct{}{}
	for _, series := range counter.series {
		for key := range series {
			dimensionSet[key] = struct{}{}
		}
	}
	dimensions := make([]string, 0, len(dimensionSet))
	for key := range dimensionSet {
		dimensions = append(dimensions, key)
	}
	sort.Strings(dimensions)
	for _, dimension := range dimensions {
		groups := map[string]map[string]struct{}{}
		for _, series := range counter.series {
			var others strings.Builder
			for _, other := range dimensions {
				if other != dimension {
					others.WriteString(series[other])
					others.WriteByte(0)
				}
			}
			key := others.String()
			values := groups[key]
			if values == nil {
				values = map[string]struct{}{}
				groups[key] = values
			}
			values[series[dimension]] = struct{}{}
		}
		for _, values := range groups {
			if len(values) == esTermsCap {
				return dimension, true
			}
		}
	}
	return "", false
}
