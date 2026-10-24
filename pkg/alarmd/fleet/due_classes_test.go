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
	"encoding/json"
	"fmt"
	"reflect"
	"testing"
	"time"
)

func dueIDs(prefix string, count int) []string {
	ids := make([]string, 0, count)
	for index := 0; index < count; index++ {
		ids = append(ids, fmt.Sprintf("%s-%04d", prefix, index))
	}
	return ids
}

// Each due list gets the store's per-list budget, like the owned set: a list
// past it keeps its longest sorted prefix and says it was cut, so a reader
// treats it as at least that many and never as the whole. Lists within the
// budget are written whole and not marked.
func TestTheStoreCutsEachDueListToItsBudgetAndSaysSo(t *testing.T) {
	store := &RedisStore{maxAnomalyBytes: 200}
	snapshot := Snapshot{Replica: "r1", TakenAt: time.Unix(10_000, 0), Owned: 3,
		Due: &DueClasses{Late: dueIDs("late", 40), Overdue: dueIDs("overdue", 2), Never: dueIDs("never", 40)}}
	written, err := store.written(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	due := written.Due
	if due == nil {
		t.Fatal("written snapshot lost its due classes")
	}
	if !due.LateCut || len(due.Late) >= 40 || len(due.Late) == 0 {
		t.Errorf("late: %d listed, cut %v; want a cut prefix", len(due.Late), due.LateCut)
	}
	if !due.NeverCut || len(due.Never) >= 40 {
		t.Errorf("never: %d listed, cut %v; want a cut prefix", len(due.Never), due.NeverCut)
	}
	if due.OverdueCut || !reflect.DeepEqual(due.Overdue, dueIDs("overdue", 2)) {
		t.Errorf("overdue within budget: %v cut %v; want it whole and unmarked", due.Overdue, due.OverdueCut)
	}
	if !reflect.DeepEqual(due.Late, dueIDs("late", len(due.Late))) {
		t.Errorf("late kept %v, want the sorted prefix", due.Late)
	}
	// The caller's lists are not shortened in place.
	if len(snapshot.Due.Late) != 40 {
		t.Errorf("the caller's late list was cut to %d", len(snapshot.Due.Late))
	}
}

// The due classes travel in the snapshot under one key; a build that has no
// due index writes none, which reads as "not listed", never as "nothing late".
func TestDueClassesRoundTripAndAreAbsentWithoutAnIndex(t *testing.T) {
	with := Snapshot{Replica: "r1", Due: &DueClasses{Late: []string{"a"}, LateCut: true}}
	encoded, err := json.Marshal(with)
	if err != nil {
		t.Fatal(err)
	}
	var back Snapshot
	if err := json.Unmarshal(encoded, &back); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back.Due, with.Due) {
		t.Errorf("round trip = %+v, want %+v", back.Due, with.Due)
	}
	without, err := json.Marshal(Snapshot{Replica: "r1"})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(without, &fields); err != nil {
		t.Fatal(err)
	}
	if _, present := fields["due"]; present {
		t.Errorf("a snapshot with no due index wrote %s", fields["due"])
	}
}
