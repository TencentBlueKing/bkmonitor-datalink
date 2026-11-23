// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package window

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metricmetadata/core"
)

func TestPendingRecordsTimestampAndConsumptionOrder(t *testing.T) {
	record := func(field, fingerprint string, timestamp int64, hasTimestamp bool) core.Record {
		return core.Record{
			GroupID: 1, ScopeHash: "scope", FieldName: field, Fingerprint: fingerprint,
			TimestampMS: timestamp, HasTimestamp: hasTimestamp,
		}
	}

	var pending PendingRecords
	stale := pending.Append([]core.Record{
		record("cpu", "first", 20, true),
		record("memory", "memory", 0, false),
		record("cpu", "older", 10, true),
		record("cpu", "same-time-last", 20, true),
	})
	if stale != 1 {
		t.Fatalf("stale records = %d, want 1", stale)
	}
	stored := pending.Records()
	if len(stored) != 2 || stored[0].Fingerprint != "same-time-last" || stored[1].FieldName != "memory" {
		t.Fatalf("records no longer preserve first-seen key order: %+v", stored)
	}
	got := pending.Ordered()
	if len(got) != 2 || got[0].FieldName != "memory" || got[1].Fingerprint != "same-time-last" {
		t.Fatalf("ordered records after timestamp merge = %+v", got)
	}

	pending.Append([]core.Record{record("cpu", "missing-time-last", 0, false)})
	got = pending.Ordered()
	if got[1].Fingerprint != "missing-time-last" {
		t.Fatalf("missing timestamp did not use consumption order: %+v", got)
	}

	pending.Replace([]core.Record{record("memory", "replacement", 0, false)})
	got = pending.Ordered()
	if len(got) != 1 || got[0].Fingerprint != "replacement" {
		t.Fatalf("replace retained old records or indexes: %+v", got)
	}
}
