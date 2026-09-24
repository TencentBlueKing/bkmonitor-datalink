// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane_test

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A cutover that rewrites a timeline writes the timeline's new revision onto
// the Query Group's Assignment record in the same script, where the record
// exists; a record that does not exist is not conjured. The record's word
// is what a holder's renewal brings back, so it has to be written beside the
// timeline, never a step later.
func TestACutoverStampsTheTimelineRevisionOnTheAssignmentRecord(t *testing.T) {
	client := newControlplaneRedis(t)
	repository, err := controlplane.NewRedisCatalogRepository(client, "alarmd:control:stamp", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	recordKey := func(queryGroup execution.QueryGroupIdentity) string {
		return "alarmd:ownership:assignment:" + string(queryGroup)
	}
	repository.WithAssignmentRecordKey(recordKey)
	ctx := context.Background()

	oldCatalog := validCatalog(t, 80)
	oldSnapshot, _, err := repository.PublishCatalog(ctx, oldCatalog)
	if err != nil {
		t.Fatal(err)
	}
	queryGroup := oldCatalog.QueryGroups[0].Identity
	// No record yet: the Query Group has not been placed. The initial
	// activation writes its timeline and must not conjure a record for it -
	// a record with only this field would read as a placed Query Group with
	// no worker.
	oldOpen := frozenSchedule(t, oldSnapshot.Publication, oldCatalog.QueryGroups[0], 60, nil)
	oldActivation := activationState(t, 1, oldSnapshot, oldOpen, nil)
	if err := repository.CompareAndSetInitialScheduleActivation(ctx, controlplane.ActivationExpectation{}, oldActivation,
		[]execution.InitialScheduleActivationFact{{Segment: oldOpen.Segment}}); err != nil {
		t.Fatal(err)
	}
	if exists, err := client.Exists(ctx, recordKey(queryGroup)).Result(); err != nil || exists != 0 {
		t.Fatalf("the initial activation created the record (exists=%d, %v); a record is a placement's to create", exists, err)
	}
	// Placed since, with whatever the ownership store writes; the cutover
	// then stamps the record that exists.
	if err := client.HSet(ctx, recordKey(queryGroup), "desired_worker_id", "w1", "record_revision", "3").Err(); err != nil {
		t.Fatal(err)
	}

	newCatalog := catalogWithSchedule(t, oldCatalog, 120, 30)
	newSnapshot, _, err := repository.PublishCatalog(ctx, newCatalog)
	if err != nil {
		t.Fatal(err)
	}
	boundary := execution.EvaluationTime(180)
	oldClosed := frozenSchedule(t, oldSnapshot.Publication, oldCatalog.QueryGroups[0], 60, &boundary)
	newOpen := frozenSchedule(t, newSnapshot.Publication, newCatalog.QueryGroups[0], boundary, nil)
	newActivation := activationState(t, 2, newSnapshot, newOpen, nil)
	cutover := execution.ScheduleCutoverFact{OldSegment: oldClosed.Segment, NewSegment: newOpen.Segment}
	if err := repository.CompareAndSetScheduleCutover(ctx, controlplane.ActivationExpectation{
		RecordRevision: oldActivation.RecordRevision, Current: oldActivation.Current,
	}, newActivation, []execution.ScheduleCutoverFact{cutover}); err != nil {
		t.Fatal(err)
	}
	stamped, err := client.HGet(ctx, recordKey(queryGroup), "timeline_record_revision").Result()
	if err != nil || stamped != "2" {
		t.Fatalf("after the cutover the record says timeline revision %q (%v), want 2", stamped, err)
	}
	if revision, err := client.HGet(ctx, recordKey(queryGroup), "record_revision").Result(); err != nil || revision != "3" {
		t.Fatalf("the record's own revision moved to %q (%v); the timeline revision is the Query Group's, not a decision", revision, err)
	}
}
