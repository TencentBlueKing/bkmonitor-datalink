// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package uq

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/sourceliveness"
)

// buildProbeWire is a source liveness probe as the provider takes it: the
// structured endpoint the strategy's own query uses, one clause counting the
// source over windows of one period, step one period, no condition and no
// grouping. The scope is the query's: its space, or for a global business
// Plan the space skipped (scopeHeaders). Never a raw query with limit 0,
// which a Doris-backed index set turns into a SELECT * of millions of rows.
func buildProbeWire(probe sourceliveness.Probe) (string, []byte, map[string]string, error) {
	functions, err := mapFunctions(probe.Clause.Functions)
	if err != nil {
		return "", nil, nil, err
	}
	timeAggregation, err := mapTimeAggregation(probe.Clause.TimeAggregation)
	if err != nil {
		return "", nil, nil, err
	}
	start := probe.Key.Start
	if probe.QueryStart > start {
		start = probe.QueryStart
	}
	if probe.Key.StepSeconds <= 0 || start >= probe.Key.End {
		return "", nil, nil, fmt.Errorf("alarmd access uq: a source liveness probe needs a window")
	}
	scope := execution.QueryPlanFacts{TenantID: probe.Key.TenantID, SpaceScope: probe.Key.SpaceScope, GlobalBusiness: probe.Key.GlobalBusiness}
	clause := probe.Clause
	body := request{TSDBMap: probe.TSDBMap, QueryList: []queryClause{{
		DataSource: clause.DataSource, TableID: clause.TableID, FieldName: clause.FieldName, Driver: clause.Driver,
		TimeField: clause.TimeField, ReferenceName: clause.ReferenceName, Functions: functions, TimeAggregation: timeAggregation,
		QueryString: clause.QueryString,
	}}, MetricMerge: sourceliveness.ProbeReference,
		StartTime: strconv.FormatInt(start, 10), EndTime: strconv.FormatInt(probe.Key.End, 10),
		Step: strconv.FormatInt(probe.Key.StepSeconds, 10) + "s", SpaceUID: bodySpace(scope), Timezone: probe.Timezone,
		// Buckets start where the window starts, so the two periods are the
		// two the window names and not a grid that cuts one of them.
		NotTimeAlign: true}
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", nil, nil, fmt.Errorf("alarmd access uq: encode source liveness probe: %w", err)
	}
	return "/query/ts", encoded, scopeHeaders(scope), nil
}
