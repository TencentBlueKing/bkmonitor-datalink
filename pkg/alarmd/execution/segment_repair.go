// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package execution

import "github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"

// SegmentRepairKind says what the Control Leader found where a Query Group's
// Schedule timeline should have been, when it opened a fresh Segment at its
// own boundary instead of carrying the timeline on.
type SegmentRepairKind string

const (
	// SegmentRepairUnreadable: the timeline was there and its bytes did not
	// decode. Every Segment it held is gone with it.
	SegmentRepairUnreadable SegmentRepairKind = "unreadable"
	// SegmentRepairAbsent: the timeline's key was gone - evicted, expired,
	// deleted - and the cutover opened a new timeline in its place.
	SegmentRepairAbsent SegmentRepairKind = "absent"
)

// SegmentRepair marks a Schedule Segment the Control Leader opened in place of
// a timeline it could not carry on. It is persisted on the Segment, so the
// Worker whose cursor stood inside the lost Segments can tell the jump to this
// one apart from the two other jumps of the same shape - a retirement's hole
// and a pruned prefix - and record it under its own word rather than not at
// all. A build without the field ignores it and jumps silently, as before.
type SegmentRepair struct {
	Kind SegmentRepairKind `json:"kind"`
	// AtUnixMilli is when the Leader wrote the new timeline.
	AtUnixMilli int64 `json:"at_unix_ms"`
}

// ReasonCode is the word the cursor's skip into the marked Segment is
// recorded under, and false for a kind this build does not know: such a
// Segment is read as unmarked, which is the behaviour before the mark.
func (repair SegmentRepair) ReasonCode() (ReasonCode, bool) {
	switch repair.Kind {
	case SegmentRepairUnreadable:
		return ReasonCode(contract.ReasonScheduleRepaired), true
	case SegmentRepairAbsent:
		return ReasonCode(contract.ReasonScheduleReopened), true
	default:
		return "", false
	}
}
