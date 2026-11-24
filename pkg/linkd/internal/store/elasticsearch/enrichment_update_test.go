// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearchstore

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"linkd/internal/domain"
	"linkd/internal/store"
	"linkd/internal/store/storetest"
)

func TestEnrichmentUpdateUsesExactFullDocumentBudget(t *testing.T) {
	for _, completed := range []bool{false, true} {
		for _, large := range []bool{false, true} {
			e := storetest.Event("tenant", "size-event", "fp", "warning")
			e.SourceRawData = domain.JSONObject{"raw": json.RawMessage(`{ "escaped":"<>&\u2028", "number":9007199254740993, "null":null }`)}
			e.ExtraData = domain.JSONObject{"nested": json.RawMessage(` {"x":[true,false,{"y":"\n"}]} `)}
			e.Title = "<>&\n "
			if large {
				e.SourceRawData["large"] = json.RawMessage(`"` + strings.Repeat("x", 1<<20) + `"`)
			}
			if completed {
				var err error
				e, err = e.WithEnrichment(storetest.Enrichment(e))
				if err != nil {
					t.Fatal(err)
				}
			}
			p := store.NewUnprocessedEventProcessing()
			full, err := encodeEventDocument(e, p)
			if err != nil {
				t.Fatal(err)
			}
			body, size, err := encodeEnrichmentUpdate(e, p)
			if err != nil {
				t.Fatal(err)
			}
			if size != len(full) {
				t.Fatalf("completed=%v large=%v budget=%d full=%d", completed, large, size, len(full))
			}
			want, err := json.Marshal(struct {
				Doc domain.EventEnrichment `json:"doc"`
			}{e.EventEnrichment})
			if err != nil {
				t.Fatal(err)
			}
			if string(body) != string(want) {
				t.Fatal("wire update changed")
			}
		}
	}
	// 新增丰富字段必须显式加入遮蔽结构，否则可能重复计量或漏计量。
	fields := reflect.TypeFor[domain.EventEnrichment]()
	shadow := reflect.TypeFor[eventWithoutEnrichment]()
	for i := 0; i < fields.NumField(); i++ {
		f := fields.Field(i)
		g, ok := shadow.FieldByName(f.Name)
		if !ok || len(g.Index) != 1 || g.Tag.Get("json") != f.Tag.Get("json") && strings.Split(g.Tag.Get("json"), ",")[0] != strings.Split(f.Tag.Get("json"), ",")[0] {
			t.Fatalf("enrichment field %s not explicitly shadowed", f.Name)
		}
	}
}

func TestEnrichmentBudgetBoundaryStillRejectsBeforeWrite(t *testing.T) {
	for _, delta := range []int{-1, 0} {
		repo, fixture, e := newSnapshotEventFixture(t)
		current, err := repo.GetEvent(t.Context(), e.BKTenantID, e.EventID)
		if err != nil {
			t.Fatal(err)
		}
		result := storetest.Enrichment(e)
		updated, err := store.ApplyEventEnrichment(current, result)
		if err != nil {
			t.Fatal(err)
		}
		full, err := encodeEventDocument(updated, current.Processing)
		if err != nil {
			t.Fatal(err)
		}
		repo.config.MaxDocumentBytes = len(full) + delta
		_, err = repo.CompareAndSetEventEnrichmentFromSnapshot(t.Context(), current, result)
		if delta < 0 {
			if err == nil || fixture.writes != 0 {
				t.Fatal("oversized document was written")
			}
		} else if err != nil || fixture.writes != 1 {
			t.Fatalf("exact budget rejected: %v", err)
		}
	}
}

func BenchmarkEnrichmentUpdateEncoding(b *testing.B) {
	e := storetest.Event("tenant", "encoding-event", "fp", "warning")
	e.SourceRawData = domain.JSONObject{"raw": json.RawMessage(`"` + strings.Repeat("x", 64<<10) + `"`)}
	result := storetest.Enrichment(e)
	result.Enrich.Evaluations[0].Data["processors"] = json.RawMessage(`[{"metric":{"status":"succeeded","value":{"large":"` + strings.Repeat("y", 128<<10) + `"}}}]`)
	var err error
	e, err = e.WithEnrichment(result)
	if err != nil {
		b.Fatal(err)
	}
	p := store.NewUnprocessedEventProcessing()
	for _, reuse := range []bool{false, true} {
		name := "full_then_update"
		if reuse {
			name = "reuse_enrichment"
		}
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if reuse {
					if _, _, err := encodeEnrichmentUpdate(e, p); err != nil {
						b.Fatal(err)
					}
				} else {
					if _, err := encodeEventDocument(e, p); err != nil {
						b.Fatal(err)
					}
					if _, err := json.Marshal(map[string]any{"doc": e.EventEnrichment}); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
