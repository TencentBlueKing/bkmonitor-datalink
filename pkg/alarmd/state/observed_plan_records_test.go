// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package state

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// The whole-record absence memory is the shape this build reads and no longer
// writes, so no write path produces one to read back; an observer still has
// to read it where a Plan's memory predates the change. Each reader refuses a
// record of another schema rather than reading zeros out of it.
func TestAnObserverReadsTheWholeRecordAndRefusesAnotherSchema(t *testing.T) {
	identity := noDataIdentityV2()
	raw, err := json.Marshal(noDataEnvelope{Schema: executionNoDataSchema, Version: 1, Identity: identity, MarkerRevision: 4,
		ApplyVersion: applyVersion(), ScheduleRevision: "plan-r1", RosterVersion: "TARGET_STATIC/1",
		Groups: []execution.NoDataGroupMemory{{GroupKey: "a", LastSeen: 940}, {GroupKey: "b", LastSeen: 1000}, {GroupKey: "c", LastSeen: 1000}}})
	if err != nil {
		t.Fatal(err)
	}
	record, named, err := ObservedNoDataWhole(raw)
	if err != nil || named != identity {
		t.Fatalf("ObservedNoDataWhole() = (%+v, %+v, %v)", record, named, err)
	}
	if want := (ObservedPlanRecord{MarkerRevision: 4, EvaluationTime: 60, ScheduleRevision: "plan-r1", Groups: 3}); record != want {
		t.Fatalf("whole record = %+v, want %+v", record, want)
	}
	other := []byte(`{"schema":"alarmd-something-else","marker_revision":9}`)
	for name, read := range map[string]func([]byte) error{
		"gap marker":     func(raw []byte) error { _, _, err := ObservedGapMarker(raw); return err },
		"per-group head": func(raw []byte) error { _, _, err := ObservedNoDataHeader(raw); return err },
		"whole record":   func(raw []byte) error { _, _, err := ObservedNoDataWhole(raw); return err },
	} {
		if err := read(other); !errors.Is(err, ErrObservedRecordInvalid) {
			t.Fatalf("%s of another schema: %v, want refused", name, err)
		}
		if err := read([]byte("not json")); !errors.Is(err, ErrObservedRecordInvalid) {
			t.Fatalf("%s of bytes that are not a record: %v, want refused", name, err)
		}
	}
}
