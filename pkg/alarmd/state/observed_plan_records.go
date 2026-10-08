// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package state

import (
	"encoding/json"
	"errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// NoDataHeaderField is the per-group no-data record's header field, for a
// reader that reads the header alone rather than the whole record.
const NoDataHeaderField = noDataHeaderField

// ErrObservedRecordInvalid is a stored Plan record an observer could not read
// as one of this build's.
var ErrObservedRecordInvalid = errors.New("state: stored Plan record is not one this build reads")

// ObservedPlanRecord is what an observer reads of a Plan's gap marker or
// no-data memory: the revision each write stamps, the Slot and the schedule
// it was written under, and how much it holds -- never the scopes or the
// groups themselves. Whether a Plan's record survives from one round to the
// next is read from these beside the key's remaining life.
type ObservedPlanRecord struct {
	MarkerRevision   uint64 `json:"marker_revision"`
	EvaluationTime   int64  `json:"evaluation_time,omitempty"`
	ScheduleRevision string `json:"schedule_revision,omitempty"`
	// Scopes is how many gap scopes a marker holds; Groups how many groups a
	// whole-record no-data memory holds. The per-group record's group count
	// is its field count, which the reader has without decoding.
	Scopes int `json:"scopes,omitempty"`
	Groups int `json:"groups,omitempty"`
	// PresentAsOf and TrackingExhaustedAt are the per-group no-data header's.
	PresentAsOf         int64 `json:"present_as_of,omitempty"`
	TrackingExhaustedAt int64 `json:"tracking_exhausted_at,omitempty"`
}

// ObservedGapMarker reads a gap marker as stored, with the identity it names.
func ObservedGapMarker(raw []byte) (ObservedPlanRecord, execution.PlanGapIdentity, error) {
	var envelope gapEnvelope
	if json.Unmarshal(raw, &envelope) != nil || envelope.Schema != executionGapSchemaV2 {
		return ObservedPlanRecord{}, execution.PlanGapIdentity{}, ErrObservedRecordInvalid
	}
	return ObservedPlanRecord{MarkerRevision: envelope.MarkerRevision, EvaluationTime: int64(envelope.ApplyVersion.EvaluationTime),
		ScheduleRevision: string(envelope.ScheduleRevision), Scopes: len(envelope.Scopes)}, envelope.Identity, nil
}

// ObservedNoDataHeader reads the header field of a per-group no-data record,
// with the identity it names.
func ObservedNoDataHeader(raw []byte) (ObservedPlanRecord, execution.PlanNoDataIdentity, error) {
	var header noDataHashHeader
	if json.Unmarshal(raw, &header) != nil || header.Schema != executionNoDataSchema {
		return ObservedPlanRecord{}, execution.PlanNoDataIdentity{}, ErrObservedRecordInvalid
	}
	return ObservedPlanRecord{MarkerRevision: header.MarkerRevision, EvaluationTime: int64(header.ApplyVersion.EvaluationTime),
		ScheduleRevision: string(header.ScheduleRevision), PresentAsOf: header.PresentAsOf,
		TrackingExhaustedAt: header.TrackingExhaustedAt}, header.Identity, nil
}

// ObservedNoDataWhole reads a whole-record no-data memory, the shape this
// build reads and no longer writes, with the identity it names.
func ObservedNoDataWhole(raw []byte) (ObservedPlanRecord, execution.PlanNoDataIdentity, error) {
	var envelope noDataEnvelope
	if json.Unmarshal(raw, &envelope) != nil || envelope.Schema != executionNoDataSchema {
		return ObservedPlanRecord{}, execution.PlanNoDataIdentity{}, ErrObservedRecordInvalid
	}
	return ObservedPlanRecord{MarkerRevision: envelope.MarkerRevision, EvaluationTime: int64(envelope.ApplyVersion.EvaluationTime),
		ScheduleRevision: string(envelope.ScheduleRevision), Groups: len(envelope.Groups)}, envelope.Identity, nil
}
