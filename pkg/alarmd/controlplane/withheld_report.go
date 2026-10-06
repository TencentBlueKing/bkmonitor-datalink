// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import "sort"

// WithheldLineBudget bounds how many withheld objects one round names.
//
// It is a line budget rather than a rule about what matters: what does not fit
// is counted and reported, so a reader can tell a report that fitted from one
// that was cut. The number covers a full first round on the deployments this
// runs on today with headroom, and it bounds a round on a deployment many
// times larger - where the first round after a leader election would otherwise
// name every rejected strategy at once.
const WithheldLineBudget = 2000

// WithheldReport is what one refresh round has to say about the objects it did
// not accept, and how much of it fitted.
//
// Lines and Dropped are reported together on purpose. A capped report that does
// not say how much it cut reads as a complete one, and the reader has no way to
// tell a deployment with eleven problems from a deployment with eleven thousand.
type WithheldReport struct {
	Lines   []ObjectDisposition
	Dropped int
}

// withheldIdentity is what makes two records the same object across rounds.
// The scope is part of it: one strategy can be withheld at the plan and at a
// level, and those are two facts, not one changing its mind.
type withheldIdentity struct {
	SourceID string
	Scope    string
	LevelID  uint32
}

// ChangedWithheld is the objects whose disposition this round differs from what
// the last published audit recorded, capped.
//
// Only the differences, because the steady state is the answer nobody needs
// repeated: a deployment with two hundred rejected strategies would otherwise
// write two hundred identical lines every refresh, and the one strategy that
// started failing this morning would be somewhere in the middle of the two
// hundredth copy.
//
// A nil previous reports everything. Which rounds pass nil is the caller's
// decision and not this function's: see SourceReconciler.namedWithheld. It is
// not "the audit is missing" - the audit is published state that outlives a
// leader, so keying on it would mean a new leader reports only what changed
// since a list nobody in that process ever wrote down.
//
// Records that stopped being withheld are not reported. This answers "what is
// being held back and why", and a strategy that is now accepted is not being
// held back; the counts are where a reader sees the total move.
//
// The cap is a line budget, not a filter: what does not fit is counted, so a
// reader can tell a report that fitted from one that was cut. It is not a
// parameter, because there is no caller that should be choosing one and no
// deployment where naming every changed object at once is right; a test that
// wants to watch the cut happen calls changedWithheldWithin with a budget it
// can build a fixture for.
func ChangedWithheld(current, previous []ObjectDisposition) WithheldReport {
	return changedWithheldWithin(current, previous, WithheldLineBudget)
}

// changedWithheldWithin is ChangedWithheld against a stated budget.
//
// The budget is a count of lines, so a budget of none names none and reports
// everything as cut. There is no second reading where a budget of none means
// no budget: that would be one value carrying two opposite meanings, and the
// one production uses is neither.
func changedWithheldWithin(current, previous []ObjectDisposition, limit int) WithheldReport {
	was := make(map[withheldIdentity]ObjectDisposition, len(previous))
	for _, record := range previous {
		was[identityOf(record)] = record
	}
	changed := make([]ObjectDisposition, 0, len(current))
	for _, record := range current {
		before, known := was[identityOf(record)]
		// The field is part of what was said. The same reason at a different
		// field is a different refusal, and a report that treated the two as
		// one would go quiet on the change that matters most.
		if known && before.Disposition == record.Disposition && before.Reason == record.Reason &&
			before.FieldPath == record.FieldPath {
			continue
		}
		changed = append(changed, record)
	}
	// Sorted so the same round always reports in the same order, and so a cap
	// cuts the same tail rather than whichever entries the map happened to
	// yield: a report that drops a different arbitrary subset each round would
	// let a strategy hide by never being in the first N.
	sort.Slice(changed, func(left, right int) bool {
		if changed[left].SourceID != changed[right].SourceID {
			return changed[left].SourceID < changed[right].SourceID
		}
		if changed[left].Scope != changed[right].Scope {
			return changed[left].Scope < changed[right].Scope
		}
		return changed[left].LevelID < changed[right].LevelID
	})
	if len(changed) > limit {
		return WithheldReport{Lines: changed[:limit], Dropped: len(changed) - limit}
	}
	return WithheldReport{Lines: changed}
}

// RememberNamed is what a process has named, after a round that named justNamed
// out of current.
//
// Two things go in it: the records it had already named that are still withheld
// under the same disposition and reason, and the ones this round wrote out. A
// record the line budget cut is in neither, and that is the point -- the budget
// defers rather than suppresses. Left out of the memory, a cut record is still
// unsaid, so the next round names it, and a first round on a deployment with
// far more withheld objects than one round may name drains a budget at a time
// instead of losing the remainder to a count.
//
// A record that stopped being withheld, or that is withheld for a new reason,
// is dropped: the first is no longer something to report and the second is a
// change the next round should name again.
func RememberNamed(named, current, justNamed []ObjectDisposition) []ObjectDisposition {
	is := make(map[withheldIdentity]ObjectDisposition, len(current))
	for _, record := range current {
		is[identityOf(record)] = record
	}
	remembered := make([]ObjectDisposition, 0, len(named)+len(justNamed))
	for _, record := range named {
		if now, present := is[identityOf(record)]; present && now == record {
			remembered = append(remembered, record)
		}
	}
	return append(remembered, justNamed...)
}

func identityOf(record ObjectDisposition) withheldIdentity {
	return withheldIdentity{SourceID: record.SourceID, Scope: record.Scope, LevelID: record.LevelID}
}
