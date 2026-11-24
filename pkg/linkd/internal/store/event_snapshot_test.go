// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package store_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

func sourceSnapshot(tb testing.TB) store.StoredEvent {
	tb.Helper()
	e := storetest.Event("tenant", "snapshot", "fingerprint", "warning")
	e.SourceRawData = domain.JSONObject{"source": json.RawMessage(`"` + strings.Repeat("x", 128<<10) + `"`)}
	var err error
	e, err = e.Normalize()
	if err != nil {
		tb.Fatal(err)
	}
	return store.StoredEvent{Event: e, Processing: store.NewUnprocessedEventProcessing(), Version: store.NewVersionToken("test")}
}

func TestImmutableEventUpdatesPreserveSourceAndIsolateNewState(t *testing.T) {
	current := sourceSnapshot(t)
	before := current.Event.Clone()
	at := current.Event.ReceivedAt.Add(time.Second)
	result := store.EventResult{State: domain.EventProcessStateRejected, Outcome: "rejected", ProcessedAt: at}
	full, fp, err := store.ApplyEventResult(current, result)
	if err != nil {
		t.Fatal(err)
	}
	fast, sp, err := store.ApplyEventResultFromSnapshot(current, result)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(full, fast) || !reflect.DeepEqual(fp, sp) || !reflect.DeepEqual(current.Event, before) {
		t.Fatal("state/source differs")
	}
	isolated := fast.Clone()
	isolated.SourceRawData["source"][0] = '!'
	full.SourceRawData["source"][0] = '!'
	if !reflect.DeepEqual(current.Event, before) {
		t.Fatal("public clone leaked mutation")
	}
	enrichment := storetest.Enrichment(current.Event)
	frozen, err := store.ApplyEventEnrichmentFromSnapshot(current, enrichment)
	if err != nil {
		t.Fatal(err)
	}
	enrichment.Enrich.Evaluations[0].Data["processors"][0] = '!'
	if frozen.Enrich.Evaluations[0].Data["processors"][0] == '!' || !reflect.DeepEqual(current.Event, before) {
		t.Fatal("new enrichment ownership leaked")
	}
	if _, err := store.ApplyEventEnrichmentFromSnapshot(store.StoredEvent{Event: fast, Processing: sp}, storetest.Enrichment(current.Event)); err == nil {
		t.Fatal("enrichment allowed after final state")
	}
}

func BenchmarkEventResultSourcePayload(b *testing.B) {
	current := sourceSnapshot(b)
	result := store.EventResult{State: domain.EventProcessStateRejected, Outcome: "rejected", ProcessedAt: current.Event.ReceivedAt.Add(time.Second)}
	for _, fast := range []bool{false, true} {
		name := "isolated"
		if fast {
			name = "snapshot"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				var err error
				if fast {
					_, _, err = store.ApplyEventResultFromSnapshot(current, result)
				} else {
					_, _, err = store.ApplyEventResult(current, result)
				}
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
