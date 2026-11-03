// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package obevidence

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/openepisodes"
)

// A strategy's record of the episodes alarmd opened reads as what the
// strategy-wide closes count: its start, its entries by version, their
// openings, and how many opened past the least a close vouches from. A
// strategy with no record reads missing; a request without its tenant is
// refused rather than read for every tenant.
func TestOpenEpisodesReadsAStrategysRecordAsTheClosesCountIt(t *testing.T) {
	client := redisForTest(t)
	const prefix = "alarmd-test"
	store, err := openepisodes.NewStore(client, prefix)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Unix(1_800_000_000, 0)
	key := openepisodes.Key{TenantID: "system", StrategyID: "802"}
	var changes []openepisodes.Change
	for i := 0; i < 3; i++ {
		opened := start.Add(time.Duration(i) * time.Hour) // 0h, 1h, 2h past the start
		changes = append(changes, openepisodes.Change{Key: key, Fingerprint: fmt.Sprintf("%032x", i), Open: true,
			Entry: openepisodes.Entry{Version: fmt.Sprint(1 + i/2), OpenedAt: opened.Unix()}})
	}
	if err := store.Apply(context.Background(), changes, nil); err != nil {
		t.Fatal(err)
	}
	service := New(Options{OpenEpisodes: binding(client, "runtime", prefix)})
	r := service.Store(context.Background(), StoreRequest{Family: FamilyOpenEpisodes, StrategyID: "802", Tenant: "system"})
	summary, ok := r.Value.(OpenEpisodesSummary)
	if r.Status != "ok" || !ok {
		t.Fatalf("result %+v", r)
	}
	if !summary.Exists || summary.Entries != 3 || summary.ByVersion["1"] != 2 || summary.ByVersion["2"] != 1 ||
		!summary.Since.Equal(start) || summary.OpenedAfterAnHour != 2 || summary.TTLMS <= 0 ||
		!summary.OpenedFirst.Equal(start) || !summary.OpenedLast.Equal(start.Add(2*time.Hour)) {
		t.Fatalf("summary %+v", summary)
	}
	if r.Location.Key != store.KeyOf(key) {
		t.Fatalf("read %q, the record is %q", r.Location.Key, store.KeyOf(key))
	}
	if r := service.Store(context.Background(), StoreRequest{Family: FamilyOpenEpisodes, StrategyID: "256", Tenant: "system"}); r.Status != "missing" {
		t.Fatalf("a strategy with no record reads %s", r.Status)
	}
	if r := service.Store(context.Background(), StoreRequest{Family: FamilyOpenEpisodes, StrategyID: "802"}); r.Status != "invalid_input" {
		t.Fatalf("a request without its tenant reads %s", r.Status)
	}
}

// A strategy's record lists entry by entry, in fingerprint order, each with
// the revision it opened at and when: a reconciliation whose counts match
// the link's by revision but whose alerts do not can name the fingerprint.
// A version narrows the list to that revision, an
// unreadable entry listed under every version since its own cannot be read;
// a page holds at most limit entries and names the cursor of the next. The
// count read refuses a version, a cursor or a limit rather than ignore them.
func TestOpenEpisodeEntriesListTheRecordByFingerprintAPageAtATime(t *testing.T) {
	client := redisForTest(t)
	const prefix = "alarmd-test"
	store, err := openepisodes.NewStore(client, prefix)
	if err != nil {
		t.Fatal(err)
	}
	start := time.Unix(1_800_000_000, 0)
	key := openepisodes.Key{TenantID: "system", StrategyID: "803"}
	var changes []openepisodes.Change
	for i, version := range []string{"1", "2", "1", "2", "1"} {
		changes = append(changes, openepisodes.Change{Key: key, Fingerprint: fmt.Sprintf("%032x", i), Open: true,
			Entry: openepisodes.Entry{Version: version, OpenedAt: start.Add(time.Duration(i) * time.Minute).Unix()}})
	}
	if err := store.Apply(context.Background(), changes, nil); err != nil {
		t.Fatal(err)
	}
	unreadable := fmt.Sprintf("%032x", 9)
	if err := client.HSet(context.Background(), store.KeyOf(key), unreadable, "{not json").Err(); err != nil {
		t.Fatal(err)
	}
	service := New(Options{OpenEpisodes: binding(client, "runtime", prefix)})
	list := func(request StoreRequest) (Result, OpenEpisodeEntries) {
		t.Helper()
		request.Family, request.StrategyID, request.Tenant = FamilyOpenEpisodeEntries, "803", "system"
		r := service.Store(context.Background(), request)
		page, _ := r.Value.(OpenEpisodeEntries)
		return r, page
	}
	r, page := list(StoreRequest{Version: "1", Limit: 2})
	if r.Status != "ok" || page.Matching != 4 || len(page.Entries) != 2 || page.NextCursor != fmt.Sprintf("%032x", 2) ||
		page.Entries[0] != (OpenEpisodeEntry{Fingerprint: fmt.Sprintf("%032x", 0), Version: "1", OpenedAt: start.UTC()}) ||
		page.Entries[1].Fingerprint != fmt.Sprintf("%032x", 2) || page.Entries[1].OpenedAt != start.Add(2*time.Minute).UTC() {
		t.Fatalf("version 1, first page: %s %+v", r.Status, page)
	}
	_, page = list(StoreRequest{Version: "1", Limit: 2, Cursor: page.NextCursor})
	if len(page.Entries) != 2 || page.Entries[0].Fingerprint != fmt.Sprintf("%032x", 4) ||
		page.Entries[1] != (OpenEpisodeEntry{Fingerprint: unreadable, Unreadable: true}) || page.NextCursor != "" {
		t.Fatalf("version 1, last page: %+v, want the last version-1 entry and the unreadable one, no cursor", page)
	}
	_, page = list(StoreRequest{Version: "2"})
	if page.Matching != 3 || len(page.Entries) != 3 || page.Entries[0].Version != "2" || page.Entries[1].Version != "2" || page.NextCursor != "" {
		t.Fatalf("version 2: %+v", page)
	}
	_, page = list(StoreRequest{})
	if page.Matching != 6 || len(page.Entries) != 6 {
		t.Fatalf("every version: %+v", page)
	}
	for name, request := range map[string]StoreRequest{
		"a limit past the bound": {Family: FamilyOpenEpisodeEntries, StrategyID: "803", Tenant: "system", Limit: MaxEpisodeEntries + 1},
		"no tenant":              {Family: FamilyOpenEpisodeEntries, StrategyID: "803"},
		"a version on the count": {Family: FamilyOpenEpisodes, StrategyID: "803", Tenant: "system", Version: "1"},
		"a limit on the count":   {Family: FamilyOpenEpisodes, StrategyID: "803", Tenant: "system", Limit: 10},
	} {
		if r := service.Store(context.Background(), request); r.Status != "invalid_input" {
			t.Fatalf("%s reads %s, want invalid_input", name, r.Status)
		}
	}
}
