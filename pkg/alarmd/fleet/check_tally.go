// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import "time"

// mergeCheckTallies adds one replica's rows' folds into another's, in
// everything the rows half writes: counts add, strategy and business sets
// take their union, the columns the rows came from add up, and of the
// moments the earliest failure and the latest failure, success, record and
// skip are kept.
func mergeCheckTallies(into, from checkTallies) {
	for check, part := range from {
		entry := into.ensure(check)
		entry.objects += part.objects
		unionInto(entry.strategies, part.strategies)
		unionInto(entry.businesses, part.businesses)
		entry.columns |= part.columns
		entry.demoted += part.demoted
		entry.current += part.current
		entry.retained += part.retained
		entry.lastHour += part.lastHour
		if part.newest.After(entry.newest) {
			entry.newest = part.newest
		}
		if part.skipped != nil {
			if entry.skipped == nil {
				entry.skipped = &Consequence{}
			}
			entry.skipped.Skipped += part.skipped.Skipped
			entry.skipped.SkippedRecent += part.skipped.SkippedRecent
			entry.skipped.SkippedNewest = latest(entry.skipped.SkippedNewest, part.skipped.SkippedNewest)
		}
		if part.reasons != nil && entry.reasons == nil {
			entry.reasons = map[string]int{}
		}
		for reason, n := range part.reasons {
			entry.reasons[reason] += n
		}
		if part.onsets != nil && entry.onsets == nil {
			entry.onsets = map[time.Time]int{}
		}
		for minute, n := range part.onsets {
			entry.onsets[minute] += n
		}
		entry.withoutOnset += part.withoutOnset
		for key, group := range part.groups {
			target := entry.groups[key]
			if target == nil {
				target = &CheckGroup{Key: key}
				entry.groups[key] = target
			}
			mergeCheckGroup(target, group)
			if sets, known := part.groupSets[key]; known {
				targetSets, has := entry.groupSets[key]
				if !has {
					targetSets = [2]map[string]struct{}{{}, {}}
					entry.groupSets[key] = targetSets
				}
				unionInto(targetSets[0], sets[0])
				unionInto(targetSets[1], sets[1])
			}
		}
	}
}

// mergeCheckGroup adds one replica's fold of a group into another's, in
// what noteProblem writes.
func mergeCheckGroup(into, from *CheckGroup) {
	into.Objects += from.Objects
	if from.Codes != nil && into.Codes == nil {
		into.Codes = map[string]int{}
	}
	for code, n := range from.Codes {
		into.Codes[code] += n
	}
	into.Overdue += from.Overdue
	into.Retrying += from.Retrying
	into.FailingNow += from.FailingNow
	into.CompletingNow += from.CompletingNow
	into.Delayed += from.Delayed
	into.Silent += from.Silent
	into.Stalled += from.Stalled
	into.FirstFailure = earliest(into.FirstFailure, from.FirstFailure)
	into.LastFailure = latest(into.LastFailure, from.LastFailure)
	into.LastSuccess = latest(into.LastSuccess, from.LastSuccess)
}

func unionInto(into, from map[string]struct{}) {
	for key := range from {
		into[key] = struct{}{}
	}
}

func earliest(left, right *time.Time) *time.Time {
	if right == nil || (left != nil && !right.Before(*left)) {
		return left
	}
	at := *right
	return &at
}

func latest(left, right *time.Time) *time.Time {
	if right == nil || (left != nil && !right.After(*left)) {
		return left
	}
	at := *right
	return &at
}
