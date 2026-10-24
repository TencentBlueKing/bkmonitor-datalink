// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package execution

import (
	"context"
	"encoding/json"
	"time"
)

// The no-data end index keeps, per Plan, the series whose alert on a level
// written to end on no data (strategy.RecoveryPlan.MissingData) is open: what
// a round needs to end that alert once the series has gone from the answer,
// when there is no record left to take it from. It is written as the series
// is evaluated and read back each round; an entry leaves when its alert ends
// or is no longer open.

// NoDataEndKey names one Plan's index: its identity under its State
// generation, so a generation the Plan moved off is not read.
type NoDataEndKey struct {
	Plan            PlanIdentity
	StateGeneration StateGeneration
}

// NoDataEndEntry is one (series, level) whose alert is open: the alert's
// fingerprint, the alert dimensions the series' last record had, and the
// source time of its last real point (RuntimeLevelStateMutation
// .LastRealPointTime) - a source time, not the round that read it.
//
// AttributedBusinessID is, for a global business Plan, the business its
// last record was attributed to (TriggerEventV1.AttributedBusinessID), read
// while the series still reported: the business its alert was raised under,
// which the series' absent end is filed under too. Empty on every other
// Plan's entries, and on an entry written before it was kept, whose end is
// then filed under the Plan's own business.
type NoDataEndEntry struct {
	Series               SeriesIdentityDigest
	LevelID              uint32
	Fingerprint          string
	Dimensions           map[string]json.RawMessage
	LastRealPoint        int64
	AttributedBusinessID string
}

// NoDataEndEntryKey names one entry of a Plan's index.
type NoDataEndEntryKey struct {
	Series  SeriesIdentityDigest
	LevelID uint32
}

// Key is the entry's name.
func (entry NoDataEndEntry) Key() NoDataEndEntryKey {
	return NoDataEndEntryKey{Series: entry.Series, LevelID: entry.LevelID}
}

// NoDataEndUpdate is what evaluating a series said about its entry: Open
// keeps it with these values, otherwise it is removed - the alert ended this
// round, or the consumer's set says it is not open. Judged is false when
// neither this round nor the consumer's set could say whether the alert is
// open - a set not yet read after a handover - and then the entry is only
// refreshed if it is there, never created or removed.
type NoDataEndUpdate struct {
	Entry  NoDataEndEntry
	Open   bool
	Judged bool
}

// OpenAlertJudge is an open alert set that can say whether its answer is a
// judgement: held is membership, judged whether the set was read whole and
// recently enough to answer for the strategy at all. The no-data end index
// acts only on judged answers: "not in a set nobody has read yet" is not
// "closed".
type OpenAlertJudge interface {
	Holds(tenantID, strategyID, fingerprint string) (held, judged bool)
}

// OpenAlertLink is an open alert set that says whether any consumer's set
// stands behind it. One that is not linked can never judge, so nothing that
// acts only on judged answers - the no-data end index - is kept beside it:
// its entries would be writes no round could ever read for an end.
type OpenAlertLink interface {
	Linked() bool
}

// NoDataEndLoaded is one Plan's index as read: Unavailable when it could not
// be read, which a round reads as nothing to decide on, never as an empty
// index; Undecodable counts entries this build could not read, which are
// left to their alert and removed with it.
type NoDataEndLoaded struct {
	Key         NoDataEndKey
	Entries     []NoDataEndEntry
	Unavailable bool
	Undecodable int
}

// NoDataEndWrite is one Plan's change to its index: entries put and entries
// removed, and how long the index lives without another write.
type NoDataEndWrite struct {
	Key     NoDataEndKey
	Puts    []NoDataEndEntry
	Deletes []NoDataEndEntryKey
	TTL     time.Duration
}

// NoDataEndIndex is where the index is kept. Optional: a deployment without
// it ends a level on no data only while its group is in the answer.
type NoDataEndIndex interface {
	LoadNoDataEnds(context.Context, []NoDataEndKey) ([]NoDataEndLoaded, error)
	ApplyNoDataEnds(context.Context, NoDataEndWrite) error
}
