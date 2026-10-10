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
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

// windowGroups is count Query Group names of one length, so their timelines
// are of one size.
func windowGroups(count int) []execution.QueryGroupIdentity {
	groups := make([]execution.QueryGroupIdentity, count)
	for index := range groups {
		groups[index] = execution.QueryGroupIdentity(fmt.Sprintf("qg-window-%04d", index))
	}
	return groups
}

// A cutover reads the timelines it walks a window ahead of the walk, never
// all of them first: at most 512 are read and not yet walked, whatever the
// number of Query Groups, and each window is one pipeline of lengths and
// one of values. Doubling the Query Groups doubles the round trips, not
// what is held at once. Each timeline handed over is the stored bytes,
// decoded.
func TestACutoverReadsItsTimelinesAWindowAheadOfTheWalk(t *testing.T) {
	for _, count := range []int{1024, 2048} {
		groups := windowGroups(count)
		repository, client := timelineCacheFixture(t, groups, 2)
		client.gets, client.strlens, client.roundTrips = 0, 0, 0
		timelines := repository.cutoverTimelines(groups)
		ahead := 0
		for index, queryGroup := range groups {
			timeline, raw, err := timelines.timeline(context.Background(), queryGroup)
			if err != nil {
				t.Fatalf("%d groups: %s: %v", count, queryGroup, err)
			}
			if string(raw) != client.values[repository.scheduleTimelineKey(queryGroup)] || timeline.QueryGroup != queryGroup {
				t.Fatalf("%d groups: %s handed %s and bytes that are not its own", count, queryGroup, timeline.QueryGroup)
			}
			ahead = max(ahead, client.gets-(index+1))
		}
		if ahead != 511 {
			t.Fatalf("%d groups: at most %d timelines were read ahead of the walk, want 511", count, ahead)
		}
		if client.gets != count || client.strlens != count || client.roundTrips != 2*count/512 {
			t.Fatalf("%d groups: %d GETs, %d STRLENs in %d round trips; want %d, %d in %d", count,
				client.gets, client.strlens, client.roundTrips, count, count, 2*count/512)
		}
	}
}

// A window holds at most the cutover's read bound of timeline bytes - the
// timeline cache's bound, from the container - or the one timeline larger
// than it: three timelines of a bound three and a half long are a window
// each, and a bound shorter than one timeline reads them one at a time.
// Each timeline's length is read once, in one pipeline ahead of the
// windows, not again for every window that did not take it.
func TestACutoverWindowHoldsAtMostItsBoundOrOneTimeline(t *testing.T) {
	groups := windowGroups(30)
	for _, test := range []struct {
		name      string
		timelines func(size int) int
		window    int
	}{
		{name: "three and a half", timelines: func(size int) int { return size * 7 / 2 }, window: 3},
		{name: "shorter than one", timelines: func(size int) int { return size / 2 }, window: 1},
	} {
		repository, client := timelineCacheFixture(t, groups, 2)
		size := len(client.values[repository.scheduleTimelineKey(groups[0])])
		repository.controlCache = newControlReadCache(controlTimelineCacheDefaultMaxEntries, test.timelines(size))
		client.gets, client.strlens, client.roundTrips = 0, 0, 0
		windows := map[int]int{}
		client.onGet = func(key string) {
			if len(client.values[key]) != size {
				t.Fatalf("%s: %s is not of the one size", test.name, key)
			}
			windows[client.roundTrips]++
		}
		timelines := repository.cutoverTimelines(groups)
		for _, queryGroup := range groups {
			if _, _, err := timelines.timeline(context.Background(), queryGroup); err != nil {
				t.Fatalf("%s: %s: %v", test.name, queryGroup, err)
			}
		}
		if len(windows) != len(groups)/test.window {
			t.Fatalf("%s: %d windows %v, want %d of %d", test.name, len(windows), windows, len(groups)/test.window, test.window)
		}
		for trip, read := range windows {
			if read != test.window {
				t.Fatalf("%s: round trip %d read %d timelines, want %d", test.name, trip, read, test.window)
			}
		}
		if client.strlens != len(groups) || client.roundTrips != 1+len(groups)/test.window {
			t.Fatalf("%s: %d lengths in %d round trips, want %d in one pipeline and one pipeline of values a window",
				test.name, client.strlens, client.roundTrips, len(groups))
		}
	}
}

// The walk asks for the timelines in the order it was given them; asking
// out of that order is refused rather than answered with another's bytes.
// A key that is not there is ErrScheduleUnavailable, as its single read
// was, and a transport failure fails the cutover as a dependency.
func TestACutoverReadOutOfOrderIsRefusedAndAFailureIsTheDependencys(t *testing.T) {
	groups := windowGroups(3)
	repository, client := timelineCacheFixture(t, groups, 2)
	delete(client.values, repository.scheduleTimelineKey(groups[1]))
	timelines := repository.cutoverTimelines(groups)
	if _, _, err := timelines.timeline(context.Background(), groups[1]); err == nil {
		t.Fatal("the second timeline was handed before the first")
	}
	if _, _, err := timelines.timeline(context.Background(), groups[0]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := timelines.timeline(context.Background(), groups[1]); !errors.Is(err, ErrScheduleUnavailable) {
		t.Fatalf("a missing timeline read %v, want ErrScheduleUnavailable", err)
	}
	if _, _, err := timelines.timeline(context.Background(), groups[2]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := timelines.timeline(context.Background(), groups[2]); err == nil {
		t.Fatal("a timeline past the last was handed")
	}

	client.getError = errors.New("connection reset")
	var dependency *ActivationDependencyIOError
	if _, _, err := repository.cutoverTimelines(groups).timeline(context.Background(), groups[0]); !errors.As(err, &dependency) {
		t.Fatalf("a failed read = %v, want the dependency's", err)
	}
}

// A window Redis answered a key of with an error fails the cutover as the
// dependency's when the walk reaches it, as the key's single read did: the
// answer says nothing about the timeline, which is not read as missing and
// opened again, and nothing in the window is handed over. So does a window
// of which Redis answered every key with an error, as while it loads.
func TestACutoverWindowRedisAnswersWithAnErrorIsTheDependencys(t *testing.T) {
	for _, test := range []struct {
		name     string
		answered func(repository *RedisCatalogRepository, groups []execution.QueryGroupIdentity) map[string]error
		want     string
	}{
		{name: "one key of another type", want: "WRONGTYPE",
			answered: func(repository *RedisCatalogRepository, groups []execution.QueryGroupIdentity) map[string]error {
				return map[string]error{repository.scheduleTimelineKey(groups[1]): answeredError("WRONGTYPE Operation against a key holding the wrong kind of value")}
			}},
		{name: "every key while loading", want: "LOADING",
			answered: func(repository *RedisCatalogRepository, groups []execution.QueryGroupIdentity) map[string]error {
				answered := map[string]error{}
				for _, group := range groups {
					answered[repository.scheduleTimelineKey(group)] = answeredError("LOADING Redis is loading the dataset in memory")
				}
				return answered
			}},
	} {
		groups := windowGroups(3)
		repository, client := timelineCacheFixture(t, groups, 2)
		client.answered = test.answered(repository, groups)
		var dependency *ActivationDependencyIOError
		_, _, err := repository.cutoverTimelines(groups).timeline(context.Background(), groups[0])
		if !errors.As(err, &dependency) || errors.Is(err, ErrScheduleUnavailable) || !strings.Contains(err.Error(), test.want) {
			t.Fatalf("%s: the window read %v, want the dependency's %s", test.name, err, test.want)
		}
	}
}
