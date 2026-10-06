// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"context"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/progress"
)

// progressRestoreSource reads what the control plane already recorded about an
// object, so a replica that just started does not have to watch a fresh round
// before it can say anything.
//
// It reads the same Progress the scheduler reads, one object at a time and only
// for objects this replica owns and has not yet determined. The publisher bounds
// how many it asks for per tick.
func progressRestoreSource(store *progress.Store) func(context.Context, execution.QueryGroupIdentity) (fleet.RestoredState, error) {
	if store == nil {
		return nil
	}
	return func(ctx context.Context, queryGroup execution.QueryGroupIdentity) (fleet.RestoredState, error) {
		result, err := store.LoadProgress(ctx, execution.ProgressIdentity{QueryGroup: queryGroup})
		if err != nil {
			return fleet.RestoredState{}, err
		}
		if result.Progress == nil {
			return fleet.RestoredState{}, nil
		}
		return restoredStateOf(*result.Progress), nil
	}
}

// restoredStateOf maps one Progress record onto what the tracker restores
// from. Every Slot on the record is zero for "the record names none" -- a
// record from before the field existed, or one a build without it wrote back
// during a mixed-version roll -- and none of them is turned into a timestamp
// at the epoch: the tracker would read an age of decades off it.
func restoredStateOf(progress execution.ScheduleProgress) fleet.RestoredState {
	restored := fleet.RestoredState{
		LastCompletion: string(progress.LastCompletionKind),
		NextSlot:       time.Unix(int64(progress.NextSlot), 0),
	}
	// Zero means no round has ever completed in full, which is a different
	// statement from "it last completed in full at the epoch".
	if progress.LastFullSlot > 0 {
		restored.LastFullSlot = time.Unix(int64(progress.LastFullSlot), 0)
	}
	// The same for the two facts about records: the last Slot known to have
	// had them, and the first Slot of the run of empty rounds.
	if progress.LastDataSlot > 0 {
		restored.LastDataSlot = time.Unix(int64(progress.LastDataSlot), 0)
	}
	if progress.EmptyRunSinceSlot > 0 {
		restored.EmptyRunSince = time.Unix(int64(progress.EmptyRunSinceSlot), 0)
	}
	restored.LastRound = restoredRoundOf(progress.LastCompletion)
	return restored
}

// restoredRoundOf maps the commit's summary of the last round onto what the
// tracker restores from. Nil in, nil out: a record from before the field
// existed has no round to speak of, and a summary at Slot zero would read as
// a round that happened. The commit's clock is RFC3339 by contract; a value
// that does not parse leaves the time zero rather than inventing one, and
// the row then keeps the reason and drops the clock.
func restoredRoundOf(summary *execution.LastCompletionSummary) *fleet.RestoredRound {
	if summary == nil {
		return nil
	}
	round := &fleet.RestoredRound{
		Slot: time.Unix(int64(summary.Slot), 0), Kind: string(summary.Kind), ReasonCode: string(summary.ReasonCode),
		SnapshotRevision: string(summary.Contract.SnapshotRevision), QueryRevision: string(summary.Contract.QueryRevision),
		ScheduleRevision: string(summary.Contract.ScheduleRevision),
	}
	if completedAt, err := time.Parse(time.RFC3339Nano, summary.CompletedAt); err == nil {
		round.CompletedAt = completedAt
	}
	for _, resolution := range summary.TargetResolutions {
		restored := fleet.RestoredTargetResolution{StrategyID: resolution.StrategyID, State: resolution.State,
			NodesMissing: resolution.NodesMissing, NodesForeign: resolution.NodesForeign, StaleAgeSeconds: resolution.StaleAgeSeconds}
		for _, failure := range resolution.Failures {
			restored.Failures = append(restored.Failures, fleet.RestoredSelectorFailure{
				Kind: failure.Kind, ID: failure.ID, Reason: failure.Reason, Dropped: failure.Dropped, Kept: failure.Kept})
		}
		round.TargetResolutions = append(round.TargetResolutions, restored)
	}
	return round
}

// fleetRestoreBudgetPerPublish is how many objects one publish may read back.
//
// It is a rate, not a cap: every owned object is eventually restored, just
// spread across publishes rather than read in one burst at the moment the
// process is least settled. At the current publish cadence a full deployment is
// covered in well under a minute, against the many minutes of unknown a restart
// otherwise costs.
const fleetRestoreBudgetPerPublish = 128

// A failed read may be retried on later publishes, within the shared read budget.
// Stop after three attempts per ownership tenure rather than polling forever.
const fleetRestoreMaxAttempts = 3
