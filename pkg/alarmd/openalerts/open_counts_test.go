// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package openalerts

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// The copy counts each tracked strategy's open alerts as the gate reads them,
// and lists the strategies it cannot count -- a set not loaded yet -- apart:
// those are unknown, never zero. A loaded set with nothing in it is counted
// as zero, so a strategy in neither list is one the copy does not track.
func TestOpenCountsCountLoadedSetsAndNameTheUnknown(t *testing.T) {
	c := &clock{at: time.Unix(1700000000, 0)}
	options := indexOptions(c)
	sets := map[StrategyKey][]string{keyA: {"f1", "f2", "f3"}, keyB: {}}
	options.Source = setReaderFunc(func(_ context.Context, key StrategyKey) ([]string, error) { return sets[key], nil })
	cache := mustIndex(t, options)
	if err := cache.SetTracked([]StrategyKey{keyA, keyB}); err != nil {
		t.Fatal(err)
	}
	counts, unknown := cache.OpenCounts()
	if len(counts) != 0 || len(unknown) != 2 {
		t.Fatalf("before any read: counts %v unknown %v, want both unknown", counts, unknown)
	}
	cache.Refresh(context.Background())
	counts, unknown = cache.OpenCounts()
	if !reflect.DeepEqual(counts, map[StrategyKey]int{keyA: 3, keyB: 0}) || len(unknown) != 0 {
		t.Fatalf("after reading: counts %v unknown %v, want keyA 3 and keyB counted as zero", counts, unknown)
	}
}
