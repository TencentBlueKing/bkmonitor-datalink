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
	"sort"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cmdbcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
)

// Target group health states, the reader's closed words for its copy of the
// target group cache (fleet.WriterEvidence.State).
const (
	targetGroupNoneReferenced = "no_groups_referenced"
	targetGroupLoaded         = "loaded"
	targetGroupEmptiedHeld    = "emptied_held"
	targetGroupRefreshFailed  = "refresh_failed"
)

// withTargetGroups adds what this replica's dynamic group store reads of the
// target group cache to its endpoint: the groups it holds, its own reading
// of them, and the groups served past refreshes that could not read them -
// the longest-failing by name, since when and why, and all of them counted
// by reason. A replica serving a group from before Redis started
// answering its key with an error is read here in one step, not guessed
// from a Plan's stale age. Without a store the endpoint is as it was.
func withTargetGroups(endpoints func() []fleet.Endpoint, groups *cmdbcache.GroupStore, now func() time.Time) func() []fleet.Endpoint {
	if groups == nil {
		return endpoints
	}
	return func() []fleet.Endpoint {
		list := endpoints()
		for index := range list {
			if list[index].Role == fleet.EndpointTargetGroup {
				list[index].Writer = targetGroupEvidence(groups.Health(), now())
			}
		}
		return list
	}
}

// targetGroupEvidence is the store's health as the endpoint's writer
// evidence at at. A failed refresh names the state before a held emptying
// does: it is the one that says the copies are not the writer's latest.
func targetGroupEvidence(health cmdbcache.GroupHealth, at time.Time) *fleet.WriterEvidence {
	evidence := &fleet.WriterEvidence{Present: health.Loaded+health.Unavailable > 0, Count: health.Loaded}
	switch {
	case health.RefreshFailed:
		evidence.State = targetGroupRefreshFailed
	case health.EmptiedHeld > 0:
		evidence.State = targetGroupEmptiedHeld
	case health.Referenced == 0:
		evidence.State = targetGroupNoneReferenced
	default:
		evidence.State = targetGroupLoaded
	}
	if len(health.Failing) == 0 {
		return evidence
	}
	// The groups failing longest are named, and the rest counted: the list
	// rides in every replica's head, and Redis loading fails every group.
	failing := append([]cmdbcache.GroupFailure(nil), health.Failing...)
	sort.Slice(failing, func(i, j int) bool {
		if !failing[i].Since.Equal(failing[j].Since) {
			return failing[i].Since.Before(failing[j].Since)
		}
		return failing[i].ID < failing[j].ID
	})
	evidence.FailingTotal, evidence.FailingReasons = len(failing), map[string]int{}
	for index, failure := range failing {
		evidence.FailingReasons[failure.Reason]++
		if index < fleet.MaxFailingCopiesListed {
			evidence.Failing = append(evidence.Failing, fleet.FailingCopy{
				ID: failure.ID, SinceAgeSeconds: at.Sub(failure.Since).Seconds(), Reason: failure.Reason,
			})
		}
	}
	return evidence
}

// targetGroupReading is the store's health as the collector reads it at at.
func targetGroupReading(health cmdbcache.GroupHealth, at time.Time) metric.TargetGroupReading {
	reading := metric.TargetGroupReading{
		Groups: map[string]int{
			"referenced": health.Referenced, "loaded": health.Loaded, "unavailable": health.Unavailable,
			"failing": len(health.Failing), "emptied_pending": health.EmptiedPending, "emptied_held": health.EmptiedHeld,
		},
		RefreshFailed: health.RefreshFailed, UnansweredReads: health.UnansweredReads,
	}
	for _, failure := range health.Failing {
		reading.OldestFailingSeconds = max(reading.OldestFailingSeconds, at.Sub(failure.Since).Seconds())
	}
	return reading
}
