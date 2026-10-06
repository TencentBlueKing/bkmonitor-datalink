// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// A delta names what one activation changed since the revision before it. It
// can carry the cache across exactly one revision; a cache that held an older
// one has not seen the deltas in between, and the last delta alone would keep
// every timeline those rewrote. So the cache keeps timelines only across
// consecutive revisions and drops everything otherwise.
func TestControlReadCacheKeepsTimelinesOnlyAcrossConsecutiveRevisions(t *testing.T) {
	four := cachedTimelineBytes(4)
	cache := newControlReadCache(8, 16*four)
	cache.storeTimeline("5|current", "qg-a", cachedTimelineFor("qg-a", 5), 4)
	cache.storeTimeline("5|current", "qg-b", cachedTimelineFor("qg-b", 5), 4)

	// Revision 6 rewrote qg-b and this cache never saw it; the delta of 7
	// names qg-b too, but that is a coincidence of the fixture. Whatever 7
	// names, 6 is unaccounted for, so nothing survives.
	outcome, dropped, kept := cache.advance("7|current", []execution.QueryGroupIdentity{"qg-b"})
	if outcome != advanceReset || dropped != nil || kept != nil {
		t.Fatalf("crossing from revision 5 to 7 = %v dropped=%v kept=%v, want a reset with no audit maps", outcome, dropped, kept)
	}
	if cache.timelineCount() != 0 || cache.bytes != 0 {
		t.Fatalf("timelines after a skipped revision = %d (%d bytes), want none", cache.timelineCount(), cache.bytes)
	}
	if _, ok := cache.lookupTimeline("7|current", "qg-a"); ok {
		t.Fatal("qg-a, which revision 6 may have rewritten, was served across the skipped revision")
	}

	// The consecutive crossing is the one the delta is for: the named
	// timeline goes and the other stays.
	cache.storeTimeline("7|current", "qg-a", cachedTimelineFor("qg-a", 7), 4)
	cache.storeTimeline("7|current", "qg-b", cachedTimelineFor("qg-b", 7), 4)
	outcome, dropped, kept = cache.advance("8|current", []execution.QueryGroupIdentity{"qg-b"})
	if outcome != advanceApplied {
		t.Fatalf("crossing from revision 7 to 8 = %v, want the delta applied", outcome)
	}
	if len(dropped) != 1 || dropped["qg-b"] != 7 || len(kept) != 1 || kept["qg-a"] != 7 {
		t.Fatalf("dropped=%v kept=%v, want qg-b dropped and qg-a kept", dropped, kept)
	}
	if _, ok := cache.lookupTimeline("8|current", "qg-a"); !ok {
		t.Fatal("qg-a was not kept across the consecutive revision")
	}
	if _, ok := cache.lookupTimeline("8|current", "qg-b"); ok {
		t.Fatal("qg-b survived the delta that named it")
	}

	// A header behind the cached one, or one carrying no revision, is not a
	// crossing the delta can describe either.
	for _, header := range []string{"7|current", "legacy-header"} {
		cache.storeTimeline("8|current", "qg-b", cachedTimelineFor("qg-b", 8), 4)
		outcome, _, _ = cache.advance(header, nil)
		if outcome != advanceReset || cache.timelineCount() != 0 {
			t.Fatalf("crossing from revision 8 to %q = %v with %d timelines, want a reset with none", header, outcome, cache.timelineCount())
		}
	}

	// A cache holding nothing simply enters the header.
	fresh := newControlReadCache(8, 16*four)
	if outcome, _, _ := fresh.advance("9|current", nil); outcome != advanceCold {
		t.Fatalf("cold crossing = %v, want advanceCold", outcome)
	}
	if outcome, _, _ := fresh.advance("9|current", nil); outcome != advanceUnchanged {
		t.Fatalf("repeated crossing = %v, want advanceUnchanged", outcome)
	}
}
