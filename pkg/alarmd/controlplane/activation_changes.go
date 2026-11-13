// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"context"
	"slices"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// MaxActivationChangesSpan is how many activations a reader of the changes
// since a revision reads the deltas of; past it, it reads everything.
const MaxActivationChangesSpan = 16

// ActivationChanges is what the activations after a record revision wrote
// onto the Assignment records.
type ActivationChanges struct {
	// Revision is the record revision the activation header is at now, zero
	// when there is no header.
	Revision uint64
	// Complete says QueryGroups is every Query Group whose timeline, and so
	// whose record's timeline revision, an activation after the revision
	// asked about rewrote, less the activations skip passed over. It is
	// false when one of them cannot say - its delta expired, is unreadable
	// or says full - when more than MaxActivationChangesSpan passed, or when
	// there was no revision to start from: the reader then reads everything.
	Complete    bool
	QueryGroups []execution.QueryGroupIdentity
}

// ActivationChangesSince reads the activation header and the delta of every
// activation after since that skip does not pass over. Only the header is
// read when nothing was activated since: one small read.
func (repository *RedisCatalogRepository) ActivationChangesSince(
	ctx context.Context,
	since uint64,
	skip func(uint64) bool,
) (ActivationChanges, error) {
	version, err := repository.fetchControlVersion(ctx)
	if err != nil {
		return ActivationChanges{}, err
	}
	revision, _ := activationHeaderRevision(version.header)
	changes := ActivationChanges{Revision: revision}
	if since == 0 || revision < since || revision-since > MaxActivationChangesSpan {
		return changes, nil
	}
	seen := make(map[execution.QueryGroupIdentity]struct{})
	for next := since + 1; next <= revision; next++ {
		if skip != nil && skip(next) {
			continue
		}
		delta, present, err := repository.loadActivationDelta(ctx, next)
		if err != nil {
			return ActivationChanges{}, err
		}
		if !present || delta.Full {
			return changes, nil
		}
		for _, queryGroup := range delta.QueryGroups {
			if _, dup := seen[queryGroup]; !dup {
				seen[queryGroup] = struct{}{}
				changes.QueryGroups = append(changes.QueryGroups, queryGroup)
			}
		}
	}
	slices.Sort(changes.QueryGroups)
	changes.Complete = true
	return changes, nil
}

// OnRecordTimelineRevisions is told, after an activation of this process
// wrote them, the activation's record revision and the timeline revision it
// wrote onto each Query Group's Assignment record (where one exists). The
// Control Leader keeps its copy of the records by it, without reading them
// again. Set before the repository is used.
func (repository *RedisCatalogRepository) OnRecordTimelineRevisions(written func(revision uint64, timelines map[execution.QueryGroupIdentity]uint64)) {
	if repository != nil {
		repository.recordTimelinesWritten = written
	}
}

func (repository *RedisCatalogRepository) recordTimelineRevisions(revision uint64, timelines map[execution.QueryGroupIdentity]uint64) {
	if repository.assignmentRecordKey == nil || repository.recordTimelinesWritten == nil {
		return
	}
	repository.recordTimelinesWritten(revision, timelines)
}
