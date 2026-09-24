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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream"
)

// viewStreamFleetFacts is the fleet's reading of the Leader's view stream
// account, taken when the snapshot is built. Nil for a runtime without the
// stream, so the snapshot carries nothing rather than a made-up account.
func viewStreamFleetFacts(stats func() viewstream.Stats, now func() time.Time) func() *fleet.ViewStreamFacts {
	if stats == nil || now == nil {
		return nil
	}
	return func() *fleet.ViewStreamFacts {
		return viewStreamFacts(stats(), now())
	}
}

// viewStreamFacts maps the server's account onto the fleet's shape, field
// for field, and composes the sentence. Lagging keeps the server's order.
func viewStreamFacts(stats viewstream.Stats, at time.Time) *fleet.ViewStreamFacts {
	facts := &fleet.ViewStreamFacts{
		At: at, Leading: stats.Leading, ControlEpoch: stats.ControlEpoch, Revision: stats.Revision, Sessions: stats.Sessions,
		Expected: stats.Counts.Expected, Sent: stats.Counts.Sent, Acked: stats.Counts.Acked,
		Installed: stats.Counts.Installed, Switched: stats.Counts.Switched,
		Ignored: fleet.ViewStreamIgnored{UnknownVersion: stats.Ignored.UnknownVersion, UnexpectedReceiver: stats.Ignored.UnexpectedReceiver,
			DigestMismatch: stats.Ignored.DigestMismatch, StaleIncarnation: stats.Ignored.StaleIncarnation},
		Publications: stats.Publications, PublicationsSkipped: stats.PublicationsSkipped, SnapshotChunksSent: stats.SnapshotChunksSent,
		DeltasSent: stats.DeltasSent, EmptyDeltasSent: stats.EmptyDeltasSent, DeltasOversized: stats.DeltasOversized,
		Refusals:        stats.Refusals,
		PublishFailures: stats.PublishFailures, PublishFailureReason: stats.PublishFailureReason,
	}
	if !stats.PublishFailingSince.IsZero() {
		age := at.Sub(stats.PublishFailingSince).Seconds()
		facts.PublishFailingSeconds, facts.PublishFailingBeyondBound = &age, at.Sub(stats.PublishFailingSince) > fleet.ViewStreamStallBound
	}
	if !stats.NoSessionsSince.IsZero() {
		age := at.Sub(stats.NoSessionsSince).Seconds()
		facts.NoSessionsSeconds, facts.NoSessionsBeyondBound = &age, at.Sub(stats.NoSessionsSince) > fleet.ViewStreamStallBound
	}
	facts.Lagging = make([]fleet.ViewStreamLagging, 0, len(stats.Lagging))
	for _, lagging := range stats.Lagging {
		facts.Lagging = append(facts.Lagging, fleet.ViewStreamLagging{WorkerID: lagging.WorkerID, Incarnation: lagging.Incarnation,
			Failure: lagging.Failure, Connected: lagging.Connected})
	}
	facts.NotSwitched = make([]fleet.ViewStreamLagging, 0, len(stats.NotSwitched))
	for _, receiver := range stats.NotSwitched {
		facts.NotSwitched = append(facts.NotSwitched, fleet.ViewStreamLagging{WorkerID: receiver.WorkerID, Incarnation: receiver.Incarnation,
			Connected: receiver.Connected, SwitchedQueryGroups: receiver.SwitchedQueryGroups})
	}
	// The installed Workers' word on their objects, names included; the
	// list is empty rather than null when every one of them probed.
	facts.Objects = fleet.ViewStreamObjects{Probed: stats.Objects.Probed, Unprobed: stats.Objects.Unprobed, Missing: stats.Objects.Missing,
		UnprobedWorkers: append(make([]string, 0, len(stats.Objects.UnprobedWorkers)), stats.Objects.UnprobedWorkers...)}
	facts.Line = fleet.ViewStreamLine(facts)
	return facts
}
