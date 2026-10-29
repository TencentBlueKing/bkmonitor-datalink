// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT

package fleet

import (
	"context"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// unfilledRound is one empty completion of a log count, grouped or not:
// answered whole and empty, by a provider that does not zero-fill.
func unfilledRound(queryGroup string, slot time.Time) observability.Observation {
	observed := emptyAt(queryGroup, "4102", slot)
	observed.PrimaryInput = &observability.PrimaryInputFacts{Completeness: "FULL", DataState: "EMPTY", UnfilledWhenEmpty: true}
	return observed
}

// A log count, grouped or not, that answers empty for as long as the data lines wait
// is on its own line - empty; log counts aren't zero-filled, so quiet and
// missing look the same - and on neither NO_DATA_PERSISTENT nor
// EMPTY_EVERY_ROUND, whether or not it ever had data. A time series that
// answers empty the same way keeps today's line.
func TestALogCountsEmptyAnswerIsNotDataMissing(t *testing.T) {
	for _, sawData := range []bool{false, true} {
		at := &clock{at: now}
		tracker := newTracker(t, at)
		if sawData {
			tracker.Observe(context.Background(), dataAt("qg-logs", "4102", at.at))
			at.at = at.at.Add(time.Minute)
		}
		for elapsed := time.Duration(0); elapsed <= 2*time.Hour; elapsed += time.Minute {
			tracker.Observe(context.Background(), unfilledRound("qg-logs", at.at))
			at.at = at.at.Add(time.Minute)
		}
		rows := tracker.NoData()
		if len(rows) != 1 || rows[0].Kind != KindLogCountEmpty {
			t.Fatalf("saw data %t: rows %+v, want one LOG_COUNT_EMPTY row and no data-missing one", sawData, rows)
		}
		list := []Anomaly{rows[0]}
		Attribute(list, now)
		if list[0].Finding.Check != CheckLogCountEmpty {
			t.Fatalf("saw data %t: check %s, want LOG_COUNT_EMPTY", sawData, list[0].Finding.Check)
		}
		if words := checkWords[CheckLogCountEmpty]; words.State != StateDetecting || words.Action != ActionNone {
			t.Fatalf("LOG_COUNT_EMPTY reads %+v, want detecting with nothing to do", words)
		}
	}
}

// The same run of empty rounds from a time series: EMPTY_EVERY_ROUND, as it
// always was.
func TestATimeSeriesEmptyAnswerKeepsItsLine(t *testing.T) {
	at := &clock{at: now}
	tracker := newTracker(t, at)
	for elapsed := time.Duration(0); elapsed <= 2*time.Hour; elapsed += time.Minute {
		observed := emptyAt("qg-series", "4103", at.at)
		observed.PrimaryInput = &observability.PrimaryInputFacts{Completeness: "FULL", DataState: "EMPTY"}
		tracker.Observe(context.Background(), observed)
		at.at = at.at.Add(time.Minute)
	}
	rows := tracker.NoData()
	if len(rows) != 1 || rows[0].Kind != KindEmptyEveryRound {
		t.Fatalf("rows %+v, want one EMPTY_EVERY_ROUND row", rows)
	}
}
