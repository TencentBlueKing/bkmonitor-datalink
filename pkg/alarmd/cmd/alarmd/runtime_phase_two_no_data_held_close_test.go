// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The one close an absence gets when the target stops naming its group
// reaches the alert, even when the process that decides it has not yet read
// the consumer's open alert set.
//
// The capability decomposition is explicit that this close is the only one
// there will ever be (section 5.3, A8 (a): one NORMAL so the alert can
// close, then the group is forgotten - nobody will speak about it again, and
// without that one the alert stays open for good), and the user's ruling
// makes it a requirement (section 5.9 item 3 (b): when a group leaves, end
// the old alert with a recovery and do not leave it standing).
//
// Here the alert for host B was raised by worker 0 and the consumer holds it.
// Worker 1 takes the Query Group over, the target drops B, and worker 1 runs
// the first round under the new target before its copy of the consumer's open
// alert set has been read - the state every new owner is in until its copy
// catches up, after a restart or a handover. Its copy answers from what this
// process sent, which is nothing about B, and would hold an ordinary recovery
// (the self-maintain ruling). This one is not held: the round forgets B, so a
// held close would be lost for good, and a close the consumer has no alert
// for is an orphan there, which costs it nothing. The close goes on that
// round, once; the round after the copy reads the set sends nothing more.
func TestAOneTimeCloseDecidedByANewOwnerReachesTheAlert(t *testing.T) {
	strategy := lifecycleStrategy{revision: 7, continuous: 1,
		targets: []string{lifecycleHostA, lifecycleHostB}, dimensions: lifecycleHostPair}
	fixture := startLifecycleFixture(t, strategy, nil)
	fixture.serve(false, 5, lifecycleHostA)
	fixture.mustAttempt(1)
	raised := fixture.noDataEventsFor(lifecycleHostB)
	if len(raised) != 1 || raised[0].EventKind != contract.TriggerEventAbnormal {
		t.Fatalf("round 1 sent host B's no-data events %+v, want one ABNORMAL", raised)
	}

	fixture.replace("alarmd-worker-1")
	changed := strategy
	changed.revision, changed.targets = 8, []string{lifecycleHostA}
	closing := fixture.change(changed, 1)
	for round := int64(2); round < closing; round++ {
		fixture.mustAttempt(round)
	}
	closes := func() []contract.TriggerEventV1 {
		var found []contract.TriggerEventV1
		for _, event := range fixture.noDataEventsFor(lifecycleHostB) {
			if event.EventKind != contract.TriggerEventAbnormal {
				found = append(found, event)
			}
		}
		return found
	}
	fixture.mustAttempt(closing)
	if _, lastFull := fixture.progressSlots(); lastFull != fixture.evaluationAt(closing) {
		t.Fatalf("round %d did not complete: last full Slot %d", closing, lastFull)
	}
	if got := closes(); len(got) != 1 || got[0].DedupeMD5 != raised[0].DedupeMD5 {
		t.Fatalf("host B's alert got closes %+v on round %d, the first under the target without it and before "+
			"the new owner read the open alert set; want exactly one, under the alert's own identity", got, closing)
	}
	if _, _, held := fixture.absenceOf(lifecycleHostB); held {
		t.Fatalf("host B is still remembered after round %d, the round that closed it", closing)
	}

	fixture.publishOpenAlert(raised[0].DedupeMD5)
	fixture.waitOpenAlertSetRead(closing)
	fixture.mustAttempt(closing + 1)
	if got := closes(); len(got) != 1 {
		t.Fatalf("host B's alert got %d closes by round %d; the one close is sent once", len(got), closing+1)
	}
}
