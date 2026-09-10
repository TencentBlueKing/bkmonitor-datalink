// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package fleet

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

func digest(seed string) string { return strings.Repeat(seed, 64)[:64] }

// fakeWindowRedis implements just the sorted set and hash the window store uses.
// The package already fakes Redis this way rather than taking a test-server
// dependency, and the surface here is small enough that the fake stays honest.
type fakeWindowRedis struct {
	redis.Cmdable
	scores map[string]float64
	audit  map[string]string
}

func newFakeWindowRedis() *fakeWindowRedis {
	return &fakeWindowRedis{scores: map[string]float64{}, audit: map[string]string{}}
}

func (client *fakeWindowRedis) ZAdd(_ context.Context, _ string, members ...*redis.Z) *redis.IntCmd {
	for _, member := range members {
		client.scores[member.Member.(string)] = member.Score
	}
	return redis.NewIntResult(int64(len(members)), nil)
}

func (client *fakeWindowRedis) ZRem(_ context.Context, _ string, members ...interface{}) *redis.IntCmd {
	for _, member := range members {
		delete(client.scores, member.(string))
	}
	return redis.NewIntResult(int64(len(members)), nil)
}

func (client *fakeWindowRedis) ZRangeByScore(_ context.Context, _ string, by *redis.ZRangeBy) *redis.StringSliceCmd {
	low, lowOpen := parseScoreBound(by.Min, math.Inf(-1))
	high, highOpen := parseScoreBound(by.Max, math.Inf(1))
	matched := make([]string, 0, len(client.scores))
	for member, score := range client.scores {
		if (lowOpen && score <= low) || (!lowOpen && score < low) {
			continue
		}
		if (highOpen && score >= high) || (!highOpen && score > high) {
			continue
		}
		matched = append(matched, member)
	}
	sort.Slice(matched, func(left, right int) bool {
		if client.scores[matched[left]] == client.scores[matched[right]] {
			return matched[left] < matched[right]
		}
		return client.scores[matched[left]] < client.scores[matched[right]]
	})
	return redis.NewStringSliceResult(matched, nil)
}

func parseScoreBound(raw string, unbounded float64) (float64, bool) {
	open := strings.HasPrefix(raw, "(")
	raw = strings.TrimPrefix(raw, "(")
	if raw == "-inf" || raw == "+inf" {
		return unbounded, open
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return unbounded, open
	}
	return value, open
}

func (client *fakeWindowRedis) HSet(_ context.Context, _ string, values ...interface{}) *redis.IntCmd {
	if len(values) == 1 {
		if fields, ok := values[0].(map[string]any); ok {
			for field, value := range fields {
				client.audit[field] = value.(string)
			}
		}
	}
	return redis.NewIntResult(int64(len(values)), nil)
}

func (client *fakeWindowRedis) HDel(_ context.Context, _ string, fields ...string) *redis.IntCmd {
	for _, field := range fields {
		delete(client.audit, field)
	}
	return redis.NewIntResult(int64(len(fields)), nil)
}

func (client *fakeWindowRedis) HMGet(_ context.Context, _ string, fields ...string) *redis.SliceCmd {
	values := make([]interface{}, 0, len(fields))
	for _, field := range fields {
		if value, ok := client.audit[field]; ok {
			values = append(values, value)
		} else {
			values = append(values, nil)
		}
	}
	return redis.NewSliceResult(values, nil)
}

func (client *fakeWindowRedis) Expire(_ context.Context, _ string, _ time.Duration) *redis.BoolCmd {
	return redis.NewBoolResult(true, nil)
}

func windowStore(t *testing.T) *WindowStore {
	t.Helper()
	store, err := NewWindowStore(newFakeWindowRedis(), "alarmd-window-test")
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestOpeningAWindowMakesItReadableByEveryReplica(t *testing.T) {
	store := windowStore(t)
	ctx := context.Background()
	if _, err := store.Open(ctx, []string{digest("a")}, "operator", 10*time.Minute, now); err != nil {
		t.Fatal(err)
	}
	open, err := store.Load(ctx, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].QueryGroup != digest("a") {
		t.Fatalf("windows = %+v, want the one that was opened", open)
	}
	if open[0].OpenedBy != "operator" {
		t.Fatalf("opened_by = %q, want the window traceable to whoever opened it", open[0].OpenedBy)
	}
}

// A window that never ends is the static whitelist again, written from a
// different place. Expiry is what makes it a window.
func TestAWindowStopsBeingReadableOnceItExpires(t *testing.T) {
	store := windowStore(t)
	ctx := context.Background()
	if _, err := store.Open(ctx, []string{digest("a")}, "operator", time.Minute, now); err != nil {
		t.Fatal(err)
	}
	if open, err := store.Load(ctx, now.Add(59*time.Second)); err != nil || len(open) != 1 {
		t.Fatalf("windows before expiry = %+v err = %v, want it still open", open, err)
	}
	open, err := store.Load(ctx, now.Add(61*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 0 {
		t.Fatalf("windows after expiry = %+v, want none", open)
	}
}

func TestWindowsAreRefusedWhenTheyCannotBeHonoured(t *testing.T) {
	store := windowStore(t)
	ctx := context.Background()
	for name, attempt := range map[string]func() error{
		"not an identity": func() error {
			_, err := store.Open(ctx, []string{"strategy-8930"}, "operator", time.Minute, now)
			return err
		},
		"no ttl": func() error {
			_, err := store.Open(ctx, []string{digest("a")}, "operator", 0, now)
			return err
		},
		"longer than the ceiling": func() error {
			_, err := store.Open(ctx, []string{digest("a")}, "operator", MaxWindowTTL+time.Second, now)
			return err
		},
		"nobody to attribute it to": func() error {
			_, err := store.Open(ctx, []string{digest("a")}, "", time.Minute, now)
			return err
		},
	} {
		if err := attempt(); err == nil {
			t.Fatalf("%s was accepted", name)
		}
	}
}

// The budget is shared. A caller that only counted its own request would let two
// callers each stay under the cap and together exceed it.
func TestTheOpenWindowCapCountsWhatIsAlreadyOpen(t *testing.T) {
	store := windowStore(t)
	ctx := context.Background()
	first := make([]string, 0, MaxOpenWindows)
	for index := 0; index < MaxOpenWindows; index++ {
		first = append(first, fmt.Sprintf("%064x", index))
	}
	if _, err := store.Open(ctx, first, "operator", time.Minute, now); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Open(ctx, []string{digest("b")}, "someone-else", time.Minute, now); err == nil {
		t.Fatal("a window past the shared cap was accepted")
	}
	// Re-opening something already open is not a new claim on the budget.
	if _, err := store.Open(ctx, first[:1], "operator", time.Minute, now); err != nil {
		t.Fatalf("re-opening an open window was refused: %v", err)
	}
}

func TestClosingAWindowEndsItBeforeItExpires(t *testing.T) {
	store := windowStore(t)
	ctx := context.Background()
	if _, err := store.Open(ctx, []string{digest("a"), digest("b")}, "operator", time.Minute, now); err != nil {
		t.Fatal(err)
	}
	open, err := store.Close(ctx, []string{digest("a")}, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || open[0].QueryGroup != digest("b") {
		t.Fatalf("windows after close = %+v, want only the one not closed", open)
	}
}

func windowHandler(t *testing.T) http.Handler {
	t.Helper()
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
		stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: healthySnapshots()})
	handler, err := NewHandler(service, windowStore(t), func() time.Time { return now }, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func post(t *testing.T, handler http.Handler, body string) (int, map[string]any) {
	t.Helper()
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/windows", strings.NewReader(body))
	handler.ServeHTTP(response, request)
	var decoded map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode: %v (%s)", err, response.Body.String())
	}
	return response.Code, decoded
}

func TestTheAPIOpensAndReportsWindows(t *testing.T) {
	handler := windowHandler(t)
	status, body := post(t, handler,
		fmt.Sprintf(`{"query_groups":[%q],"opened_by":"operator","ttl_seconds":600}`, digest("a")))
	if status != http.StatusOK {
		t.Fatalf("status = %d body = %+v", status, body)
	}
	windows, _ := body["windows"].([]any)
	if len(windows) != 1 {
		t.Fatalf("windows = %+v, want the one opened", body["windows"])
	}

	status, body = get(t, handler, "/api/windows")
	if status != http.StatusOK {
		t.Fatalf("read status = %d", status)
	}
	if windows, _ = body["windows"].([]any); len(windows) != 1 {
		t.Fatalf("read windows = %+v, want the one opened", body["windows"])
	}
	if body["max_ttl_seconds"].(float64) != MaxWindowTTL.Seconds() {
		t.Fatalf("max_ttl_seconds = %v, want the ceiling reported so a caller need not guess", body["max_ttl_seconds"])
	}
}

func TestTheAPIRefusesWindowsItCannotHonour(t *testing.T) {
	handler := windowHandler(t)
	for name, body := range map[string]string{
		"not an identity": `{"query_groups":["strategy-8930"],"opened_by":"operator","ttl_seconds":600}`,
		"past the ceiling": fmt.Sprintf(`{"query_groups":[%q],"opened_by":"operator","ttl_seconds":%d}`,
			digest("a"), int(MaxWindowTTL.Seconds())+1),
		"unattributed":  fmt.Sprintf(`{"query_groups":[%q],"ttl_seconds":600}`, digest("a")),
		"not a request": `{`,
	} {
		if status, _ := post(t, handler, body); status != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", name, status)
		}
	}
}

// Without a store the route is absent rather than present and broken, so a
// deployment that has not wired one fails the call instead of accepting a window
// it will never apply.
func TestTheWindowRouteIsAbsentWithoutAStore(t *testing.T) {
	handler := handlerWith(t, healthySnapshots(), Expectation{QueryGroups: 949, Known: true}, replicas())
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/windows", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 when no window store is wired", response.Code)
	}
}
