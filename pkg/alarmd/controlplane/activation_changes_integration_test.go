// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"context"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

type heardActivation struct {
	revision  uint64
	timelines map[execution.QueryGroupIdentity]uint64
}

// Each activation this process makes tells what it wrote onto the records:
// its record revision and each timeline revision, the one the record then
// says. Another process reads the same activation from its delta.
func TestAnActivationTellsTheTimelineRevisionsItWroteOntoTheRecords(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:heard", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	recordKey := func(queryGroup execution.QueryGroupIdentity) string {
		return "alarmd:ownership:assignment:" + string(queryGroup)
	}
	repository.WithAssignmentRecordKey(recordKey)
	var heard []heardActivation
	repository.OnRecordTimelineRevisions(func(revision uint64, timelines map[execution.QueryGroupIdentity]uint64) {
		heard = append(heard, heardActivation{revision: revision, timelines: timelines})
	})
	ctx := context.Background()

	oldCatalog := validCatalog(t, 80)
	oldSnapshot, _, err := repository.PublishCatalog(ctx, oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	queryGroup := oldCatalog.QueryGroups[0].Identity
	oldOpen := frozenSchedule(t, oldSnapshot.Publication, oldCatalog.QueryGroups[0], 60, nil)
	if err := repository.CompareAndSetInitialScheduleActivation(ctx, controlplane.ActivationExpectation{}, activationState(t, 1, oldSnapshot, oldOpen, nil),
		[]execution.InitialScheduleActivationFact{{Segment: oldOpen.Segment}}); err != nil {
		t.Fatal(err)
	}
	if want := []heardActivation{{revision: 1, timelines: map[execution.QueryGroupIdentity]uint64{queryGroup: 1}}}; !reflect.DeepEqual(heard, want) {
		t.Fatalf("the initial activation told %+v, want %+v", heard, want)
	}
	if err := client.HSet(ctx, recordKey(queryGroup), "desired_worker_id", "w1", "record_revision", "3").Err(); err != nil {
		t.Fatal(err)
	}

	newCatalog := catalogWithSchedule(t, oldCatalog, 60, 30)
	newSnapshot, _, err := repository.PublishCatalog(ctx, newCatalog)
	if err != nil {
		t.Fatal(err)
	}
	cutOverAt(t, repository, newSnapshot, 180)
	stamped, err := client.HGet(ctx, recordKey(queryGroup), "timeline_record_revision").Result()
	if err != nil {
		t.Fatal(err)
	}
	if len(heard) != 2 || heard[1].revision != 2 || strconv.FormatUint(heard[1].timelines[queryGroup], 10) != stamped {
		t.Fatalf("the cutover told %+v, the record says %s; want revision 2 and what the record says", heard[1:], stamped)
	}
	// The cutover rewrote every active Query Group, the only one, so its
	// delta says full and another process reads everything; passed over as
	// this process's own, there is nothing to read.
	changes, err := repository.ActivationChangesSince(ctx, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := (controlplane.ActivationChanges{Revision: 2}); !reflect.DeepEqual(changes, want) {
		t.Fatalf("another process reads the cutover as %+v, want %+v", changes, want)
	}
	changes, err = repository.ActivationChangesSince(ctx, 1, func(revision uint64) bool { return revision == heard[1].revision })
	if err != nil {
		t.Fatal(err)
	}
	if want := (controlplane.ActivationChanges{Revision: 2, Complete: true}); !reflect.DeepEqual(changes, want) {
		t.Fatalf("this process reads its own cutover as %+v, want %+v", changes, want)
	}

	// A repository that does not know where the records live wrote none,
	// and tells nothing.
	unaware, err := controlplane.NewRedisCatalogRepository(newControlplaneRedis(t), "alarmd:control:unaware", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	told := false
	unaware.OnRecordTimelineRevisions(func(uint64, map[execution.QueryGroupIdentity]uint64) { told = true })
	unawareSnapshot, _, err := unaware.PublishCatalog(ctx, oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	unawareOpen := frozenSchedule(t, unawareSnapshot.Publication, oldCatalog.QueryGroups[0], 60, nil)
	if err := unaware.CompareAndSetInitialScheduleActivation(ctx, controlplane.ActivationExpectation{}, activationState(t, 1, unawareSnapshot, unawareOpen, nil),
		[]execution.InitialScheduleActivationFact{{Segment: unawareOpen.Segment}}); err != nil {
		t.Fatal(err)
	}
	if told {
		t.Fatal("a repository that writes no record told what it wrote onto them")
	}
}
