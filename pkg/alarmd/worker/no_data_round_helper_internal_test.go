// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package worker

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/nodata"
)

// noDataRoundFor is one Plan's round over the series dimensions given, its
// memory read in parts as a Slot reads it, with every series it says kept on
// the round, for a case about the round rather than the Slot.
func (stream *streamedExecution) noDataRoundFor(
	due execution.DuePlan, seen []map[string]string, completeness execution.Completeness,
) (noDataRound, error) {
	config := due.CompiledPlan.NoData()
	if config == nil {
		return noDataRound{outcome: nodata.OutcomeNone}, nil
	}
	present := nodata.NewPresentSet(config.AggDimension)
	for _, dimensions := range seen {
		present.Add(dimensions)
	}
	var series []completedSeries
	round, err := stream.decideNoDataRound(context.Background(), due, present, completeness, func(entry completedSeries) error {
		series = append(series, entry)
		return nil
	})
	round.series = series
	return round, err
}
