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
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/ownership"
)

// progressRestoreSource reads what the control plane already recorded about
// objects, so a replica that just started does not have to watch a fresh
// round before it can say anything.
//
// It reads the same Progress the scheduler reads, in one batched read, and
// only for objects this replica owns and either has not yet determined or has
// determined without ever seeing records (fleet.Tracker.WantsRestore). The
// publisher bounds how many it asks for per tick, and each record is admitted
// by the observation memory line before it is read: the answer covers the
// ones admitted, in order, and the rest are the next publish's. A record the
// line keeps refusing -- room that stays above zero and below that record's
// length -- holds the objects after it back as long as it lasts, and is seen
// as fleet_restore refusals that keep rising. An object
// whose record is missing restores nothing and is not an error; one whose
// record could not be read or decoded is an error of its own, in its place.
//
// Only a record read and found unusable -- it did not decode, named another
// object, or decoded and did not validate -- is a fact about that record.
// Redis answering its key with an error (LOADING while it loads a dataset,
// BUSY behind a script) or the read not reaching Redis says nothing about
// it, and comes back as errRestoreUnread: the publisher spends no attempt
// on it and reads it again at the next publish.
func progressRestoreSource(store progressBatchLoader, admit func(uint64) bool) func(context.Context, []execution.QueryGroupIdentity) (
	[]fleet.RestoredState, []error) {
	if store == nil {
		return nil
	}
	return func(ctx context.Context, queryGroups []execution.QueryGroupIdentity) ([]fleet.RestoredState, []error) {
		identities := make([]execution.ProgressIdentity, 0, len(queryGroups))
		for _, queryGroup := range queryGroups {
			identities = append(identities, execution.ProgressIdentity{QueryGroup: queryGroup})
		}
		results, errs, read := store.LoadProgressWithin(ctx, identities, admit)
		states := make([]fleet.RestoredState, read)
		for index := 0; index < read; index++ {
			// The error decides: a record that decoded and did not validate
			// comes with its error, and restores nothing.
			switch {
			case errs[index] == nil:
				if results[index].Progress != nil {
					states[index] = restoredStateOf(*results[index].Progress)
				}
			case !readAndUnusable(results[index], errs[index]):
				errs[index] = &errRestoreUnread{err: errs[index]}
			}
		}
		return states, errs[:read]
	}
}

// errRestoreUnread is a record the restore read did not get: Redis answered
// its key with an error, or the read did not reach Redis.
type errRestoreUnread struct{ err error }

func (err *errRestoreUnread) Error() string {
	return "alarmd: restore record unread: " + err.err.Error()
}
func (err *errRestoreUnread) Unwrap() error { return err.err }

// readAndUnusable reports whether a read's error is a fact about the record:
// one that did not decode or named another object (a deterministic control
// fact), or one that decoded and did not validate, which comes back found
// with its error.
func readAndUnusable(result execution.ProgressLoadResult, err error) bool {
	var fact interface{ DeterministicControlFact() }
	return errors.As(err, &fact) || result.Status == execution.ProgressFound
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
	if empty := summary.Empty; empty != nil {
		round.Empty = &fleet.RestoredEmptyRound{Quiet: empty.Quiet, EmptiedByTarget: empty.EmptiedByTarget,
			ContentScope: empty.ContentScope}
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

// fleetRestoreBudgetPerPublish is how many objects one publish may ask to
// read back: one pipeline of a batched control read (ownership.ControlReadBatch),
// of which each record is read only once the observation memory line admits
// its length (progressRestoreSource).
//
// It is a rate, not a cap: every owned object is eventually restored, just
// spread across publishes rather than read in one burst at the moment the
// process is least settled.
//
// What it guards is the publish itself. restoreOwned runs inline before the
// snapshot is built, and its read is two round trips -- the records' lengths,
// then the records the line admitted -- and a restarting replica's reads on
// the control-plane store to this many per interval. The bytes are the
// line's to bound: a record is typically about a kilobyte, so a batch is
// about half a megabyte, but one carrying an unfinished range may be a
// megabyte, and a batch of those is read as far as the line has room.
//
// What it costs is time to a complete view after a start: ceil(wanted/512)
// publishes while the line has room, wanted being the owned objects not yet
// determined plus those determined without records (fleet.Tracker.WantsRestore).
// At the default 5 s interval that is one or two publishes for the 700 to 900
// a replica wants on the deployments this has run on, and about three minutes
// for 20,000, within RestartCatchUpGrace; slower when the line refuses. A
// record-at-a-time read of 128 per publish took about 13 minutes for the same
// 20,000.
const fleetRestoreBudgetPerPublish = ownership.ControlReadBatch

// A failed read may be retried on later publishes, within the shared read budget.
// Stop after three attempts per ownership tenure rather than polling forever.
const fleetRestoreMaxAttempts = 3
