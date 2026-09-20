// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package platformsettings

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

// fakeSource answers with whatever the test put there, or fails.
type fakeSource struct {
	publication Publication
	err         error
	reads       int
}

func (source *fakeSource) Read(context.Context, []Field) (Publication, error) {
	source.reads++
	if source.err != nil {
		return Publication{}, source.err
	}
	return source.publication, nil
}

func published(revision string, values map[Field]string) Publication {
	publication := Publication{Published: true, Revision: revision, Values: map[Field]json.RawMessage{}}
	for field, value := range values {
		publication.Values[field] = json.RawMessage(value)
	}
	return publication
}

type cacheFixture struct {
	source *fakeSource
	cache  *Cache
	clock  time.Time
}

func newCacheFixture(t *testing.T, deployment Layer) *cacheFixture {
	t.Helper()
	fixture := &cacheFixture{source: &fakeSource{}, clock: time.Unix(1_700_000_000, 0)}
	cache, err := New(Options{Source: fixture.source, Deployment: deployment, Now: func() time.Time { return fixture.clock }})
	if err != nil {
		t.Fatal(err)
	}
	fixture.cache = cache
	return fixture
}

// Before any publication the copy answers from the deployment layer and the
// code defaults, and says never_loaded. A publication makes it
// authoritative and the platform's values win; the source going away makes
// it stale on the last publication, and past the bound the deployment is
// degraded for it; the source coming back makes it authoritative again and
// the age restarts. Every step is one that can be read off the mode.
func TestCacheFollowsThePublicationAndKeepsTheLastOne(t *testing.T) {
	seven := []string{"备用机", "测试中", "故障中", "运营中[不监控]", "开发中[不监控]", "运营中[无告警]", "开发中[无告警]"}
	fixture := newCacheFixture(t, Layer{HostDisableMonitorStates: &seven})
	ctx := context.Background()
	if stats := fixture.cache.Stats(); stats.Mode != ModeNeverLoaded || !stats.LoadedAt.IsZero() {
		t.Fatalf("stats before any refresh = %+v, want never_loaded with no load time", stats)
	}
	if current := fixture.cache.Current(); !reflect.DeepEqual(current.HostDisableMonitorStates, seven) || current.IsAccessBKData {
		t.Fatalf("current before any refresh = %+v, want the deployment layer over the defaults", current)
	}
	// Nothing published yet: the copy stays never_loaded and does not read
	// the field keys' absence as "no override".
	fixture.source.publication = Publication{}
	fixture.cache.Refresh(ctx)
	stats := fixture.cache.Stats()
	if stats.Mode != ModeNeverLoaded || stats.Unavailable[UnavailableUnpublished] != 1 || stats.Refreshes["unavailable"] != 1 {
		t.Fatalf("stats after an unpublished read = %+v", stats)
	}
	if fixture.cache.StaleBeyondBound() {
		t.Fatal("a copy that never loaded is stale")
	}
	// The platform publishes: two fields overridden, two absent.
	fixture.source.publication = published("rev-1", map[Field]string{
		FieldHostDisableMonitorStates: `["备用机"]`, FieldIsAccessBKData: `true`,
	})
	fixture.clock = fixture.clock.Add(time.Minute)
	fixture.cache.Refresh(ctx)
	stats = fixture.cache.Stats()
	current := fixture.cache.Current()
	if stats.Mode != ModeAuthoritative || stats.Revision != "rev-1" || !stats.LoadedAt.Equal(fixture.clock) ||
		stats.Refreshes["authoritative"] != 1 {
		t.Fatalf("stats after the first publication = %+v", stats)
	}
	if !reflect.DeepEqual(current.HostDisableMonitorStates, []string{"备用机"}) || !current.IsAccessBKData ||
		!reflect.DeepEqual(current.FileSystemTypeIgnore, CodeDefaults().FileSystemTypeIgnore) {
		t.Fatalf("current after the first publication = %+v", current)
	}
	if stats.Changes[FieldHostDisableMonitorStates] != 1 || stats.Changes[FieldIsAccessBKData] != 1 ||
		stats.Changes[FieldFileSystemTypeIgnore] != 0 {
		t.Fatalf("changes after the first publication = %v, want the two fields that moved", stats.Changes)
	}
	// The same publication again: nothing changes, nothing counts as a
	// change, the age restarts because the read succeeded.
	fixture.clock = fixture.clock.Add(time.Minute)
	fixture.cache.Refresh(ctx)
	if stats := fixture.cache.Stats(); stats.Changes[FieldHostDisableMonitorStates] != 1 || !stats.LoadedAt.Equal(fixture.clock) {
		t.Fatalf("stats after an unchanged publication = %+v", stats)
	}
	// The source goes away: stale on the last publication, degraded past
	// the bound, and the deployment layer does not come back in front of
	// the platform's last word.
	loadedAt := fixture.clock
	fixture.source.err = errors.New("connection refused")
	fixture.clock = fixture.clock.Add(time.Minute)
	fixture.cache.Refresh(ctx)
	stats = fixture.cache.Stats()
	if stats.Mode != ModeStale || stats.Unavailable[UnavailableReadError] != 1 || !stats.LoadedAt.Equal(loadedAt) ||
		!strings.Contains(stats.LastUnavailable, "connection refused") {
		t.Fatalf("stats after a failed read = %+v", stats)
	}
	if current := fixture.cache.Current(); !reflect.DeepEqual(current.HostDisableMonitorStates, []string{"备用机"}) {
		t.Fatalf("current after a failed read = %+v, want the last publication", current)
	}
	if fixture.cache.StaleBeyondBound() {
		t.Fatal("stale inside the bound is beyond it")
	}
	fixture.clock = loadedAt.Add(DefaultStalenessBound + time.Second)
	if !fixture.cache.StaleBeyondBound() {
		t.Fatal("stale past the bound is not beyond it")
	}
	// Back, with the override removed: the deployment layer stands again,
	// counted as a change.
	fixture.source.err = nil
	fixture.source.publication = published("rev-2", map[Field]string{FieldIsAccessBKData: `true`})
	fixture.cache.Refresh(ctx)
	stats = fixture.cache.Stats()
	current = fixture.cache.Current()
	if stats.Mode != ModeAuthoritative || stats.Revision != "rev-2" || stats.LastUnavailable != "" || fixture.cache.StaleBeyondBound() {
		t.Fatalf("stats after the source is back = %+v", stats)
	}
	if !reflect.DeepEqual(current.HostDisableMonitorStates, seven) || stats.Changes[FieldHostDisableMonitorStates] != 2 {
		t.Fatalf("current after the override was removed = %+v (changes %v), want the deployment layer", current, stats.Changes)
	}
}

// A field that does not decode refuses the whole publication: the copy
// stays on the last one (or, before any, on the deployment layer), the
// failure names the field and the value, and nothing is half applied.
func TestCacheRefusesAPublicationWithAFieldOfTheWrongType(t *testing.T) {
	fixture := newCacheFixture(t, Layer{})
	ctx := context.Background()
	for _, arm := range []struct {
		name   string
		values map[Field]string
		text   string
	}{
		{name: "a bool published as a string", values: map[Field]string{FieldIsAccessBKData: `"true"`}, text: `field is_access_bk_data: "\"true\"" is not a JSON boolean`},
		{name: "a bool published as null", values: map[Field]string{FieldIsAccessBKData: `null`}, text: `field is_access_bk_data: "null" is not a JSON boolean`},
		{name: "a list published as an object", values: map[Field]string{FieldFileSystemTypeIgnore: `{"a":1}`}, text: `field file_system_type_ignore: "{\"a\":1}" is not a JSON list`},
		{name: "a list with a number in it", values: map[Field]string{FieldHostDisableMonitorStates: `["a", 1]`}, text: `element 1 "1" is not a JSON string`},
	} {
		t.Run(arm.name, func(t *testing.T) {
			before := fixture.cache.Stats()
			fixture.source.publication = published("rev-x", arm.values)
			fixture.cache.Refresh(ctx)
			stats := fixture.cache.Stats()
			if stats.Mode != ModeNeverLoaded || stats.Unavailable[UnavailableDecodeError] != before.Unavailable[UnavailableDecodeError]+1 ||
				!strings.Contains(stats.LastUnavailable, arm.text) {
				t.Fatalf("stats = %+v, want a decode error naming %q", stats, arm.text)
			}
			if !fixture.cache.Current().Equal(CodeDefaults()) {
				t.Fatalf("current = %+v, want untouched", fixture.cache.Current())
			}
		})
	}
	// A null list is present and empty, which is a value.
	fixture.source.publication = published("rev-y", map[Field]string{FieldFileSystemTypeIgnore: `null`})
	fixture.cache.Refresh(ctx)
	if current := fixture.cache.Current(); fixture.cache.Stats().Mode != ModeAuthoritative || len(current.FileSystemTypeIgnore) != 0 {
		t.Fatalf("a null list: mode %s current %+v, want authoritative with an empty list", fixture.cache.Stats().Mode, current)
	}
}

// A deployment that renders no source is not_configured for its whole
// life, answers from the deployment layer and the defaults, and a refresh
// does nothing -- which is what alarmd did before this package existed,
// and is not a fault.
func TestCacheWithoutASourceIsNotConfigured(t *testing.T) {
	enabled := true
	cache, err := New(Options{Deployment: Layer{IsAccessBKData: &enabled}})
	if err != nil {
		t.Fatal(err)
	}
	cache.Refresh(context.Background())
	stats := cache.Stats()
	if stats.Mode != ModeNotConfigured || len(stats.Refreshes) != 0 || cache.StaleBeyondBound() {
		t.Fatalf("stats = %+v", stats)
	}
	if !cache.Current().IsAccessBKData {
		t.Fatal("the deployment layer is not in effect")
	}
	if _, err := New(Options{StalenessBound: -time.Second}); err == nil {
		t.Fatal("a negative bound was accepted")
	}
}
