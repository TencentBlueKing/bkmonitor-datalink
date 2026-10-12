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
	"sort"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// ExecutionIdentity is what one Query Group executes under at an evaluation
// time: the publication, query revision and schedule revision of the Segment
// that contains it - the three a Slot's execution contract carries - and the
// Plans that Segment activates as current.
//
// It is read from the Segment and not from the strategy directory. A Plan
// keeps running on the publication it was activated from for as long as its
// content does not change, so on a long-lived deployment most Plans run on a
// publication whose manifest is past its retention; the directory names those
// Plans without a Query Group, and the latest publication's rows name the
// Query Group under revisions no Slot runs with.
type ExecutionIdentity struct {
	SnapshotRevision execution.SnapshotRevision
	QueryRevision    execution.QueryRevision
	ScheduleRevision execution.ScheduleRevision
	Plans            []execution.PlanIdentity

	// Schedules are the Segment's frozen schedules of those Plans: when each
	// of them is due, which a reader of what ran in a window needs to know
	// what should have.
	Schedules []execution.FrozenPlanSchedule
}

// CachedExecutionIdentity answers from the timeline this process already
// holds for the Query Group at the timeline revision its lease names, and
// never reads the store: false when the control cache does not hold that
// timeline at that revision, or when no Segment of it contains at. A Query
// Group executing here has had its timeline read at its lease's revision to
// freeze its Slots, so a miss is an entry evicted, a lease that moved since
// the last Slot, or a Group not executing.
func (repository *RedisCatalogRepository) CachedExecutionIdentity(
	queryGroup execution.QueryGroupIdentity, revision uint64, at execution.EvaluationTime,
) (ExecutionIdentity, bool) {
	if repository == nil || repository.controlCache == nil || revision == 0 {
		return ExecutionIdentity{}, false
	}
	timeline, ok := repository.controlCache.peekTimelineAtRevision(queryGroup, revision)
	if !ok {
		return ExecutionIdentity{}, false
	}
	for _, segment := range timeline.Segments {
		fact := segment.Schedule.Segment
		if !fact.Contains(at) {
			continue
		}
		identity := ExecutionIdentity{SnapshotRevision: fact.Publication.SnapshotRevision,
			QueryRevision: fact.QueryRevision, ScheduleRevision: fact.ScheduleRevision}
		seen := make(map[execution.PlanIdentity]struct{}, len(segment.Plans))
		for _, record := range segment.Plans {
			plan := record.Fact.Plan
			if record.Fact.Selection != execution.ActivationCurrent || plan.Validate() != nil {
				continue
			}
			if _, duplicate := seen[plan]; duplicate {
				continue
			}
			seen[plan] = struct{}{}
			identity.Plans = append(identity.Plans, plan)
		}
		sort.Slice(identity.Plans, func(i, j int) bool { return lessPlanIdentity(identity.Plans[i], identity.Plans[j]) })
		for _, schedule := range segment.Schedule.Plans {
			if _, current := seen[schedule.Identity]; current {
				identity.Schedules = append(identity.Schedules, schedule)
			}
		}
		return identity, true
	}
	return ExecutionIdentity{}, false
}
