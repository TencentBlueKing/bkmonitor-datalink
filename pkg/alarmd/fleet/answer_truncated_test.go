// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// An object whose answered query may have been cut at the query service's
// terms cap is listed - its answer is otherwise whole, so no other line would
// - on the strategy's line, with the source and the dimension, since the
// first round that said so. A round whose query failed proves nothing and
// leaves it; the next answered round that names no cut ends it.
func TestAnAnswerThatMayHaveBeenCutIsListedOnTheStrategysLine(t *testing.T) {
	d := newDefectTracker(t, "qg-wide")
	query := func(slot int64, result observability.Result, cut bool) {
		d.tick()
		observation := observability.Observation{Component: observability.ComponentAccess, Stage: observability.StageQueryCompleted,
			Result: result, Trace: observability.TraceFields{QueryGroupKey: d.group, StrategyID: "410", BusinessID: "2", EvaluationTime: slot}}
		if cut {
			observation.QueryTruncation = []observability.QueryTruncationFacts{{Source: "custom/event", Dimension: "bk_target_ip"}}
		}
		d.tracker.Observe(context.Background(), observation)
	}
	row := func() *Anomaly {
		t.Helper()
		rows := d.tracker.NoData()
		Attribute(rows, d.at.Now())
		for index := range rows {
			if rows[index].QueryGroup == d.group {
				return &rows[index]
			}
		}
		return nil
	}
	query(60, observability.ResultSuccess, true)
	first := row()
	if first == nil || first.Kind != KindAnswerTruncated || first.Finding.Check != CheckAnswerTruncated || first.Finding.Owner != OwnerStrategy ||
		first.AnswerTruncation == nil || first.AnswerTruncation.Dimension != "bk_target_ip" || first.AnswerTruncation.Source != "custom/event" {
		t.Fatalf("after a cut answer: %+v, want ANSWER_TRUNCATED on the strategy's line naming the source and dimension", first)
	}
	since := first.Since
	query(120, observability.ResultSuccess, true)
	if again := row(); again == nil || !again.Since.Equal(since) || again.AnswerTruncation.LastSlot != 120 {
		t.Fatalf("a second cut round: %+v, want the first round's since and the latest Slot", again)
	}
	query(180, observability.ResultFailed, false)
	if still := row(); still == nil {
		t.Fatal("a failed query ended the row: it answered nothing")
	}
	query(240, observability.ResultSuccess, false)
	if ended := row(); ended != nil {
		t.Fatalf("an answered round with no cut left %+v", ended)
	}
}
