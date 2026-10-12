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
	targetGroupRefreshFailed  = "refresh_failed"
)

// withTargetGroups adds what this replica's dynamic group store reads of the
// target group cache to its endpoint: the groups it holds, its own reading
// of them, and, while its refreshes fail, since when and why - every group
// it holds is then served past them. Without a store the endpoint is as it
// was.
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
// evidence at at. A failed refresh names the state: it is the one that says
// the copies are not the writer's latest.
func targetGroupEvidence(health cmdbcache.GroupHealth, at time.Time) *fleet.WriterEvidence {
	evidence := &fleet.WriterEvidence{Present: health.Loaded+health.Unavailable > 0, Count: health.Loaded}
	switch {
	case health.RefreshFailed:
		evidence.State = targetGroupRefreshFailed
	case health.Referenced == 0:
		evidence.State = targetGroupNoneReferenced
	default:
		evidence.State = targetGroupLoaded
	}
	if !health.FailingSince.IsZero() {
		since := at.Sub(health.FailingSince).Seconds()
		evidence.FailingSinceAgeSeconds, evidence.FailingReason = &since, health.FailureReason
	}
	return evidence
}

// targetGroupReading is the store's health as the collector reads it: while
// the refreshes fail, every group held is served past them.
func targetGroupReading(health cmdbcache.GroupHealth) metric.TargetGroupReading {
	failing := 0
	if health.RefreshFailed {
		failing = health.Loaded + health.Unavailable
	}
	return metric.TargetGroupReading{
		Groups: map[string]int{
			"referenced": health.Referenced, "loaded": health.Loaded, "unavailable": health.Unavailable, "failing": failing,
		},
		RefreshFailed: health.RefreshFailed,
	}
}
