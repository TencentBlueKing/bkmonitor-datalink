// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"fmt"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// rowsFixture is a directory answer over one Plan: the published catalog's
// group for it, the records given, the current publication, and the content
// the activation round holds.
type rowsFixture struct {
	plan      execution.PlanIdentity
	published SnapshotPublicationRef
}

func publicationRef(epoch uint64) SnapshotPublicationRef {
	return SnapshotPublicationRef{SnapshotRevision: execution.SnapshotRevision(strings.Repeat(fmt.Sprint(epoch%10), 64)), PublicationEpoch: epoch}
}

func (f rowsFixture) record(on SnapshotPublicationRef, selection execution.ActivationSelection) PlanActivationRecord {
	return PlanActivationRecord{Publication: on, Fact: execution.PlanActivationFact{Plan: f.plan, Selection: selection,
		Selected: execution.ActivatedPlan{Identity: f.plan}}}
}

func (f rowsFixture) holding(group execution.QueryGroupIdentity) heldPublication {
	key := execution.PlanKeyOf(f.plan, execution.ShardOf(nil))
	return heldPublication{plans: map[execution.PlanIdentity][]heldPlan{f.plan: {{key: key, group: group, digest: execution.ObjectDigest(group + "-object")}}},
		contexts: map[execution.PlanIdentity]execution.OutputContextDigest{}}
}

func (f rowsFixture) answer(current SnapshotPublicationRef, records []PlanActivationRecord, held map[SnapshotPublicationRef]heldPublication) *directoryAnswer {
	group := QueryGroup{Identity: "qg-published", QueryPlan: execution.QueryPlanFacts{QueryRevision: "query-published"},
		ScheduleRevision: "schedule-published", Plans: []FrozenPlan{{Identity: f.plan}}}
	if held == nil {
		held = map[SnapshotPublicationRef]heldPublication{}
	}
	return &directoryAnswer{index: buildStrategyIndex(f.published, []QueryGroup{group}, nil), current: current,
		records: map[execution.PlanIdentity][]PlanActivationRecord{f.plan: records}, held: held,
		contexts: map[execution.PlanIdentity]execution.OutputContextDigest{}}
}

// A Plan's row is named from the content it runs: the current publication's,
// wherever its record sits, and a PENDING record's own. The catalog row carries the
// activation of a Plan whose content is the published catalog's, and no
// second row stands beside it; another publication's content comes from what
// the activation round holds, or the row says it is not held.
func TestADirectoryRowIsNamedFromTheContentItsPlanRuns(t *testing.T) {
	f := rowsFixture{plan: execution.PlanIdentity{TenantID: "tenant-a", BusinessID: "2", StrategyID: "1002"}, published: publicationRef(3)}
	p1, p2, p3 := publicationRef(1), publicationRef(2), f.published

	// Two cutovers that did not touch this Plan: its record sits where its
	// Segment opened, its content is the current - published - one's.
	rows := f.answer(p3, []PlanActivationRecord{f.record(p1, execution.ActivationCurrent)}, nil).drafts(f.plan)
	if len(rows) != 1 || rows[0].row.Publication != p3 || rows[0].row.QueryGroup != "qg-published" ||
		rows[0].row.Role != string(execution.ActivationCurrent) || rows[0].row.Activation == nil ||
		rows[0].row.ActivatedOn == nil || *rows[0].row.ActivatedOn != p1 || rows[0].row.ContentNotHeld {
		t.Fatalf("unchanged across two cutovers = %+v, want the catalog's row carrying its activation, activated on P1", rows)
	}

	// A PENDING record runs the publication it sits on: on the published
	// one, it is the catalog's row.
	f.published = p2
	rows = f.answer(p1, []PlanActivationRecord{f.record(p2, execution.ActivationPending)}, nil).drafts(f.plan)
	if len(rows) != 1 || rows[0].row.Role != string(execution.ActivationPending) || rows[0].row.Publication != p2 ||
		rows[0].row.QueryGroup != "qg-published" || rows[0].row.ActivatedOn != nil {
		t.Fatalf("pending on the published one = %+v, want the catalog's row carrying it", rows)
	}

	// A record older than the current publication while a later one is
	// published: the current one's content, from what is held, not the
	// catalog's.
	f.published = p3
	rows = f.answer(p2, []PlanActivationRecord{f.record(p1, execution.ActivationCurrent)},
		map[SnapshotPublicationRef]heldPublication{p2: f.holding("qg-current")}).drafts(f.plan)
	var current *StrategyDirectoryRow
	for index := range rows {
		if rows[index].row.Role == string(execution.ActivationCurrent) {
			current = &rows[index].row
		}
	}
	if current == nil || current.Publication != p2 || current.QueryGroup != "qg-current" || current.ActivatedOn == nil || *current.ActivatedOn != p1 {
		t.Fatalf("activated on P1, current P2, published P3 = %+v, want P2's content activated on P1", rows)
	}
}
