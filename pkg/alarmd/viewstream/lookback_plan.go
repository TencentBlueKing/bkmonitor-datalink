// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package viewstream

import (
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/viewstream/pb"
)

// LookbackPlanSeriesLate is the one plan class so far: the control plane has
// seen the Query Group's series arrive late at Rung.
const LookbackPlanSeriesLate = "series_late"

// LookbackPlan is one entry of the control plane's lookback plan for this
// Worker: a Query Group read late series for, the recheck rung they were
// last seen at, and when the entry stops meaning anything. It is advice
// about where to look, never a fact about the Query Group: a Worker with
// evidence of its own keeps it, and an entry past ExpiresAt is no entry.
type LookbackPlan struct {
	QueryGroup execution.QueryGroupIdentity
	Class      string
	Rung       int
	Priority   uint32
	ExpiresAt  time.Time
}

// LookbackPlansHandler takes each whole plan set the Leader sends: the set
// replaces the last one. It runs on the stream's receive path, so it must
// not block.
type LookbackPlansHandler func(plans []LookbackPlan)

// lookbackPlansFromWire is the plan set a message carries, without the
// entries no Worker could act on: no Query Group, a class this build does
// not know.
func lookbackPlansFromWire(wire *pb.LookbackPlans) []LookbackPlan {
	if wire == nil {
		return nil
	}
	plans := make([]LookbackPlan, 0, len(wire.GetEntries()))
	for _, entry := range wire.GetEntries() {
		if entry.GetQueryGroup() == "" || entry.GetClass() != LookbackPlanSeriesLate {
			continue
		}
		plans = append(plans, LookbackPlan{QueryGroup: execution.QueryGroupIdentity(entry.GetQueryGroup()),
			Class: entry.GetClass(), Rung: int(entry.GetRung()), Priority: entry.GetPriority(),
			ExpiresAt: time.UnixMilli(entry.GetExpiresAtMs())})
	}
	return plans
}

// LookbackPlansMessage is the message that carries a Worker's whole plan set
// under the Leader's control epoch. The Worker does not read the epoch: it
// holds one stream at a time and takes the last set that stream sent, and
// an entry's expiry, not its term, ends it.
func LookbackPlansMessage(controlEpoch uint64, plans []LookbackPlan) *pb.LeaderMessage {
	entries := make([]*pb.LookbackPlanEntry, 0, len(plans))
	for _, plan := range plans {
		entries = append(entries, &pb.LookbackPlanEntry{QueryGroup: string(plan.QueryGroup), Class: plan.Class,
			Rung: uint32(plan.Rung), Priority: plan.Priority, ExpiresAtMs: plan.ExpiresAt.UnixMilli()})
	}
	return &pb.LeaderMessage{Body: &pb.LeaderMessage_LookbackPlans{LookbackPlans: &pb.LookbackPlans{
		ControlEpoch: controlEpoch, Entries: entries}}}
}
