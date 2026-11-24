// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controlplane

import (
	"context"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/internal/redistest"
)

// The changes since a revision are the union of the deltas after it, read
// one by one and sorted, less the ones skipped. One that cannot say - gone,
// full - or a span past MaxActivationChangesSpan, or no revision to start
// from, or a header behind it, has the reader read everything.
func TestActivationChangesAreTheDeltasSinceOrNothingToTrust(t *testing.T) {
	server := redistest.Start(t)
	if server == nil {
		return
	}
	client := redis.NewClient(&redis.Options{Addr: server.Addr, DialTimeout: time.Second, ReadTimeout: time.Second, WriteTimeout: time.Second})
	t.Cleanup(func() { _ = client.Close() })
	repository, err := NewRedisCatalogRepository(client, "alarmd:control:changes", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	active := map[execution.QueryGroupIdentity]struct{}{"qg-a": {}, "qg-b": {}, "qg-c": {}, "qg-d": {}}
	header := func(revision uint64) {
		t.Helper()
		if err := client.Set(ctx, repository.activationHeaderKey(), strconv.FormatUint(revision, 10)+"|rest", 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	delta := func(revision uint64, changed ...execution.QueryGroupIdentity) {
		t.Helper()
		payload, err := encodeActivationDelta(revision, changed, active)
		if err != nil {
			t.Fatal(err)
		}
		if err := client.Set(ctx, repository.activationDeltaKey(revision), payload, 0).Err(); err != nil {
			t.Fatal(err)
		}
	}
	changes := func(since uint64, skip func(uint64) bool) ActivationChanges {
		t.Helper()
		got, err := repository.ActivationChangesSince(ctx, since, skip)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	for revision := uint64(1); revision <= 30; revision++ {
		delta(revision, "qg-b")
	}
	delta(11, "qg-c", "qg-a")
	delta(12, "qg-b", "qg-c")
	header(12)

	if got, want := changes(10, nil), (ActivationChanges{Revision: 12, Complete: true, QueryGroups: []execution.QueryGroupIdentity{"qg-a", "qg-b", "qg-c"}}); !reflect.DeepEqual(got, want) {
		t.Fatalf("changes since 10 = %+v, want %+v", got, want)
	}
	if got := changes(10, func(revision uint64) bool { return revision == 12 }); !got.Complete || !reflect.DeepEqual(got.QueryGroups, []execution.QueryGroupIdentity{"qg-a", "qg-c"}) {
		t.Fatalf("changes since 10 passing over 12 = %+v, want 11's alone", got)
	}
	if got := changes(12, nil); !got.Complete || len(got.QueryGroups) != 0 || got.Revision != 12 {
		t.Fatalf("changes since the header = %+v, want complete and none", got)
	}
	for name, since := range map[string]uint64{"no revision to start from": 0, "a header behind the revision": 13} {
		if got := changes(since, nil); got.Complete || got.Revision != 12 {
			t.Fatalf("%s: %+v, want the header's revision and nothing to trust", name, got)
		}
	}

	// Sixteen activations are read; seventeen are not, every delta there.
	header(28)
	if got := changes(12, nil); !got.Complete {
		t.Fatalf("sixteen activations since: %+v, want them read", got)
	}
	if got := changes(11, nil); got.Complete {
		t.Fatalf("seventeen activations since: %+v, want nothing to trust", got)
	}

	header(12)
	delta(12, "qg-a", "qg-b", "qg-c", "qg-d")
	if got := changes(10, nil); got.Complete {
		t.Fatalf("a delta that says full: %+v, want nothing to trust", got)
	}
	if err := client.Del(ctx, repository.activationDeltaKey(12)).Err(); err != nil {
		t.Fatal(err)
	}
	if got := changes(10, nil); got.Complete {
		t.Fatalf("a delta that is gone: %+v, want nothing to trust", got)
	}
}
