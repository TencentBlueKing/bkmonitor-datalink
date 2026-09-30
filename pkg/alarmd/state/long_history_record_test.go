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
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// longHistoryPoints is a day-long window at a minute's cadence: the shape of
// a strategy whose record carries fourteen hundred points a series.
const longHistoryPoints = 1469

func longHistoryRecord(t testing.TB) ([]byte, execution.StateKeyIdentity, execution.ApplyVersion) {
	t.Helper()
	identity := stateIdentityV2()
	history := make([]execution.StateHistoryPoint, 0, longHistoryPoints)
	for index := 0; index < longHistoryPoints; index++ {
		at := int64(60 * (index + 1))
		recordID, err := contract.DeriveRecordIDV2(string(identity.SeriesIdentityDigest), at)
		if err != nil {
			t.Fatal(err)
		}
		history = append(history, execution.StateHistoryPoint{RecordID: recordID, SourceTime: at,
			Levels: []execution.StateLevelFact{{LevelID: 1, DetectFingerprint: "detect", Result: execution.LevelFactNormal}}})
	}
	last := int64(60 * longHistoryPoints)
	mutation, err := execution.BuildStateMutation(execution.StateMutation{
		Identity:        identity,
		ApplyVersion:    execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: execution.EvaluationTime(last), SlotDigest: "slot-long"},
		AffectedRecords: []execution.RecordAnchor{{RecordID: history[longHistoryPoints-1].RecordID, SourceTime: last}},
		Levels: []execution.RuntimeLevelStateMutation{{LevelID: 1, LevelStateCompatibility: "compat",
			HistoryCompleteness: execution.HistoryFull, WarmupRequirementRef: "warm", LastProcessedEventTime: last}},
		BaseHistory: history[:longHistoryPoints-1], Points: history[longHistoryPoints-1:], RetentionPoints: longHistoryPoints,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encodeRuntimePacked(mutation, 1)
	if err != nil {
		t.Fatal(err)
	}
	return raw, identity, execution.ApplyVersion{StateApplyEpoch: 1, EvaluationTime: execution.EvaluationTime(last + 60), SlotDigest: "slot-next"}
}

// A record read back names every point by the id its series and time derive,
// all fourteen hundred of them: the ids are not stored, so this is what the
// read derives, point by point.
func TestALongRecordReadsBackEveryPointsDerivedID(t *testing.T) {
	raw, identity, next := longHistoryRecord(t)
	view := decodeRuntime(raw, identity, frozenRef(), next)
	if len(view.History) != longHistoryPoints {
		t.Fatalf("read back %d points (status %s %s), want %d", len(view.History), view.Status, view.ReasonCode, longHistoryPoints)
	}
	for _, point := range view.History {
		want, err := contract.DeriveRecordIDV2(string(identity.SeriesIdentityDigest), point.SourceTime)
		if err != nil {
			t.Fatal(err)
		}
		if point.RecordID != want {
			t.Fatalf("point at %d read back as %s, want the derived %s", point.SourceTime, point.RecordID, want)
		}
	}
}

// What reading one such record costs. Measured on a laptop before the
// deriver kept its series: 2.65 ms a record, most of it the per-point id.
func BenchmarkReadALongHistoryRecord(b *testing.B) {
	raw, identity, next := longHistoryRecord(b)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if view := decodeRuntime(raw, identity, frozenRef(), next); len(view.History) != longHistoryPoints {
			b.Fatalf("read back %d points", len(view.History))
		}
	}
}

// Reading a framed record through the store costs one decode of it, not two.
// The preflight used to decode the frame to classify it and then decode it
// again to build the view, which doubled the cost of every long-history Query
// Group's state read; the allocations of the store's read are held here to
// within a small margin of one decode's.
func TestTheStoreDecodesAFramedRecordOnce(t *testing.T) {
	raw, identity, next := longHistoryRecord(t)
	store := newBatchStore(t, newPipelineMemoryBackend(), nil)
	item := execution.StatePreflightItem{Identity: identity, ApplyVersion: next}
	request := execution.StatePreflightRequest{Contract: frozenRef(), Items: []execution.StatePreflightItem{item}}
	decode := testing.AllocsPerRun(5, func() { _ = decodeRuntime(raw, identity, frozenRef(), next) })
	read := testing.AllocsPerRun(5, func() {
		if view, fallback := store.readFramedRecord(request, item, raw); fallback || len(view.History) != longHistoryPoints {
			t.Fatalf("read back %d points (fallback %v)", len(view.History), fallback)
		}
	})
	if read > decode*1.25 {
		t.Fatalf("reading the record allocated %.0f times against %.0f for one decode: the frame is decoded more than once", read, decode)
	}
}
