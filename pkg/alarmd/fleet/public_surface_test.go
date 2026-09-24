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
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"
)

// The fields the public summary carries. Every other field of HealthResponse
// must come out empty; a field added later fails
// TestEveryHealthFieldIsDecidedForThePublicSummary until it is put in one of
// the two lists.
var publicHealthFields = map[string]bool{
	"Health": true, "Expected": true, "Covered": true, "Determined": true, "Unknown": true, "Healthy": true,
	"AnomaliesTotal": true, "DemotedTotal": true, "UndecidableTotal": true, "ByDesignTotal": true,
	"EmptyEveryRoundTotal": true, "Ours": true, "Unattributed": true,
	"DemotedDue": true, "DemotedDueOldestSeconds": true, "DemotionEntries": true, "DemotionExtensions": true,
	"DemotionExits": true, "LastDemotionExit": true, "PublishedVersion": true, "ReplicasNotReady": true,
	"DependenciesReplicas": true, "Cohorts": true,
	"DemotionRestored": true, "DemotionHandovers": true, "DemotionReentries": true,
}

var redactedHealthFields = map[string]bool{
	"Impact": true, "StrategyLinkBase": true, "PrunedSkips": true, "RetainedShare": true, "Workers": true,
	"Builds": true, "OutputProtocols": true, "OutputPath": true, "Cooling": true, "Degradations": true,
	"Activation": true, "NoDataHorizon": true, "Load": true, "ActivationReplica": true, "Rebalance": true,
	"RebalanceReplica": true, "AssignmentScope": true, "AssignmentScopeReplica": true, "AssignmentSweep": true,
	"AssignmentSweepReplica": true, "ViewStream": true, "ViewStreamReplica": true, "Source": true,
	"SourceReplica": true, "SourceStanding": true, "NoDataTracking": true, "Dependencies": true,
	"DependenciesReplica": true, "LinkdConsole": true, "Coverage": true, "PerReplica": true, "Overdue": true,
	"Dispatch": true, "Schedule": true, "Gaps": true, "Capacity": true,
	"VerdictHistory": true, "VerdictHistorySince": true, "VerdictHistoryReplica": true,
	"LeaderRound": true, "LeaderRoundReplica": true,
}

// fill sets every settable leaf under value to something non-zero: free-form
// strings to a planted address (a named string type is a closed word list,
// and gets a word), numbers to 7, booleans true, one element in every slice
// and map, pointers allocated.
func fill(value reflect.Value, depth int) {
	if depth > 6 {
		return
	}
	switch value.Kind() {
	case reflect.String:
		if value.Type() != reflect.TypeOf("") {
			value.SetString("WORD")
			return
		}
		value.SetString("redis.internal.example:6379")
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value.SetInt(7)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value.SetUint(7)
	case reflect.Float32, reflect.Float64:
		value.SetFloat(7)
	case reflect.Bool:
		value.SetBool(true)
	case reflect.Pointer:
		value.Set(reflect.New(value.Type().Elem()))
		fill(value.Elem(), depth+1)
	case reflect.Slice:
		slice := reflect.MakeSlice(value.Type(), 1, 1)
		fill(slice.Index(0), depth+1)
		value.Set(slice)
	case reflect.Map:
		m := reflect.MakeMap(value.Type())
		k, v := reflect.New(value.Type().Key()).Elem(), reflect.New(value.Type().Elem()).Elem()
		fill(k, depth+1)
		fill(v, depth+1)
		m.SetMapIndex(k, v)
		value.Set(m)
	case reflect.Struct:
		if value.Type() == reflect.TypeOf(time.Time{}) {
			value.Set(reflect.ValueOf(time.Date(2026, 9, 24, 0, 0, 0, 0, time.UTC)))
			return
		}
		for i := 0; i < value.NumField(); i++ {
			if value.Type().Field(i).IsExported() {
				fill(value.Field(i), depth+1)
			}
		}
	}
}

func TestEveryHealthFieldIsDecidedForThePublicSummary(t *testing.T) {
	kind := reflect.TypeOf(HealthResponse{})
	for i := 0; i < kind.NumField(); i++ {
		name := kind.Field(i).Name
		if publicHealthFields[name] == redactedHealthFields[name] {
			t.Errorf("HealthResponse.%s is in neither or both lists: decide whether the public summary carries it", name)
		}
	}
}

// Everything filled, including a planted address in every string: the
// public fields come through as they were, every other field is empty, the
// lists the page iterates are [] rather than null, and the planted address
// appears nowhere in what is sent.
func TestThePublicSummaryCarriesOnlyItsFieldsAndNoPlace(t *testing.T) {
	var full HealthResponse
	fill(reflect.ValueOf(&full).Elem(), 0)
	full.Health = HealthHealthy
	summary := PublicHealth(full)
	got, want := reflect.ValueOf(summary.HealthResponse), reflect.ValueOf(full)
	for i := 0; i < got.NumField(); i++ {
		name := got.Type().Field(i).Name
		field := got.Field(i)
		switch {
		case publicHealthFields[name]:
			if !reflect.DeepEqual(field.Interface(), want.Field(i).Interface()) {
				t.Errorf("%s = %v, want it carried as %v", name, field.Interface(), want.Field(i).Interface())
			}
		case field.Kind() == reflect.Slice:
			if field.Len() != 0 {
				t.Errorf("%s = %v, want it left out", name, field.Interface())
			}
			// The lists the page iterates are sent as [], not null.
			if field.IsNil() && !strings.Contains(got.Type().Field(i).Tag.Get("json"), "omitempty") {
				t.Errorf("%s is null, want an empty list", name)
			}
		case !field.IsZero():
			t.Errorf("%s = %+v, want it left out", name, field.Interface())
		}
	}
	raw, err := json.Marshal(summary)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "redis.internal.example") {
		t.Fatalf("summary names a place: %s", raw)
	}
	if !strings.Contains(string(raw), `"restricted":true`) || !strings.Contains(string(raw), `"health":"HEALTHY"`) {
		t.Fatalf("summary lost its verdict or its restricted mark: %.200s", raw)
	}
}

func publicWindowsCall(t *testing.T, handler http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(method, target, strings.NewReader(body)))
	return response
}

// The page's read, open and close work through the public route and show
// who, which and when -- not a sample selection.
func TestThePublicWindowRouteOpensReadsAndCloses(t *testing.T) {
	store := windowStore(t)
	handler := NewPublicWindowsHandler(store, func() time.Time { return now })
	open := `{"query_groups":["` + digest("a") + `"],"opened_by":"operator","ttl_seconds":600}`
	if got := publicWindowsCall(t, handler, http.MethodPost, "/api/windows", open); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), digest("a")) {
		t.Fatalf("open = %d %s", got.Code, got.Body.String())
	}
	got := publicWindowsCall(t, handler, http.MethodGet, "/api/windows", "")
	var listed struct {
		Windows []map[string]any `json:"windows"`
		MaxOpen int              `json:"max_open"`
	}
	if got.Code != http.StatusOK || json.Unmarshal(got.Body.Bytes(), &listed) != nil || len(listed.Windows) != 1 || listed.MaxOpen != MaxOpenWindows {
		t.Fatalf("read = %d %s", got.Code, got.Body.String())
	}
	for key := range listed.Windows[0] {
		if key != "query_group" && key != "opened_by" && key != "opened_at" && key != "expires_at" {
			t.Errorf("public window carries %q", key)
		}
	}
	closeBody := `{"query_groups":["` + digest("a") + `"],"close":true}`
	if got := publicWindowsCall(t, handler, http.MethodPost, "/api/windows", closeBody); got.Code != http.StatusOK || strings.Contains(got.Body.String(), digest("a")) {
		t.Fatalf("close = %d %s", got.Code, got.Body.String())
	}
}

// Every refusal is about the request and is decided before the store is
// asked; the store's shared cap comes back in its own words.
func TestThePublicWindowRouteRefusesWhatItMustNotDo(t *testing.T) {
	handler := NewPublicWindowsHandler(windowStore(t), func() time.Time { return now })
	qg := digest("a")
	for name, tc := range map[string]struct{ target, body, want string }{
		"sample mode":       {"/api/windows?mode=sample", `{}`, "alarmd-cli"},
		"no query group":    {"/api/windows", `{"opened_by":"x","ttl_seconds":60}`, "at least one query group"},
		"not an identity":   {"/api/windows", `{"query_groups":["nope"],"opened_by":"x","ttl_seconds":60}`, "SHA256"},
		"no name":           {"/api/windows", `{"query_groups":["` + qg + `"],"ttl_seconds":60}`, "opened_by"},
		"name too long":     {"/api/windows", `{"query_groups":["` + qg + `"],"opened_by":"` + strings.Repeat("x", PublicOpenedByMaxBytes+1) + `","ttl_seconds":60}`, "opened_by"},
		"ttl past the cap":  {"/api/windows", `{"query_groups":["` + qg + `"],"opened_by":"x","ttl_seconds":1801}`, "ttl"},
		"not a request":     {"/api/windows", `[`, "window request"},
		"more than the cap": {"/api/windows", `{"query_groups":` + manyQueryGroups(MaxOpenWindows+1) + `,"opened_by":"x","ttl_seconds":60}`, "at most"},
	} {
		got := publicWindowsCall(t, handler, http.MethodPost, tc.target, tc.body)
		if got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), tc.want) {
			t.Errorf("%s: %d %s, want 400 naming %q", name, got.Code, got.Body.String(), tc.want)
		}
	}
	// The shared cap, reached by two requests that are each under it.
	handler = NewPublicWindowsHandler(windowStore(t), func() time.Time { return now })
	half := MaxOpenWindows/2 + 1
	first := `{"query_groups":` + manyQueryGroups(half) + `,"opened_by":"x","ttl_seconds":60}`
	if got := publicWindowsCall(t, handler, http.MethodPost, "/api/windows", first); got.Code != http.StatusOK {
		t.Fatalf("first half = %d %s", got.Code, got.Body.String())
	}
	second := `{"query_groups":` + manyQueryGroupsFrom(half, half) + `,"opened_by":"x","ttl_seconds":60}`
	if got := publicWindowsCall(t, handler, http.MethodPost, "/api/windows", second); got.Code != http.StatusBadRequest || !strings.Contains(got.Body.String(), "objects may be observed at once") {
		t.Fatalf("over the shared cap = %d %s", got.Code, got.Body.String())
	}
}

func manyQueryGroups(n int) string { return manyQueryGroupsFrom(0, n) }

func manyQueryGroupsFrom(start, n int) string {
	groups := make([]string, 0, n)
	for i := start; i < start+n; i++ {
		groups = append(groups, fmt.Sprintf("%064x", i+1))
	}
	raw, _ := json.Marshal(groups)
	return string(raw)
}

// The writes one replica takes in public are bounded per minute, from
// everyone together; the next minute starts over; reads are not counted.
func TestThePublicWindowRouteBoundsWritesPerMinute(t *testing.T) {
	at := now.Truncate(time.Minute).Add(10 * time.Second)
	handler := NewPublicWindowsHandler(windowStore(t), func() time.Time { return at })
	body := `{"query_groups":["` + digest("a") + `"],"opened_by":"x","ttl_seconds":60}`
	for i := 0; i < PublicWindowWritesPerMinute; i++ {
		if got := publicWindowsCall(t, handler, http.MethodPost, "/api/windows", body); got.Code != http.StatusOK {
			t.Fatalf("write %d = %d", i+1, got.Code)
		}
	}
	got := publicWindowsCall(t, handler, http.MethodPost, "/api/windows", body)
	if got.Code != http.StatusTooManyRequests || got.Header().Get("Retry-After") != "51" {
		t.Fatalf("write past the budget = %d retry-after %q", got.Code, got.Header().Get("Retry-After"))
	}
	if got := publicWindowsCall(t, handler, http.MethodGet, "/api/windows", ""); got.Code != http.StatusOK {
		t.Fatalf("read while writes are spent = %d", got.Code)
	}
	at = at.Add(time.Minute)
	if got := publicWindowsCall(t, handler, http.MethodPost, "/api/windows", body); got.Code != http.StatusOK {
		t.Fatalf("write in the next minute = %d", got.Code)
	}
}

// failingWindowRedis fails every command with an error naming the store.
type failingWindowRedis struct{ *fakeWindowRedis }

var errStoreNamed = errors.New("dial tcp redis.internal.example:6379: connect: connection refused")

func (failingWindowRedis) ZRangeByScore(context.Context, string, *redis.ZRangeBy) *redis.StringSliceCmd {
	return redis.NewStringSliceResult(nil, errStoreNamed)
}

// A store failure is reported in fixed words on every method: its text
// names the store, and the unrestricted route would pass it on.
func TestThePublicWindowRouteNeverPassesOnAStoreError(t *testing.T) {
	store, err := NewWindowStore(failingWindowRedis{newFakeWindowRedis()}, "alarmd-window-test")
	if err != nil {
		t.Fatal(err)
	}
	handler := NewPublicWindowsHandler(store, func() time.Time { return now })
	for _, tc := range []struct{ method, body string }{
		{http.MethodGet, ""},
		{http.MethodPost, `{"query_groups":["` + digest("a") + `"],"opened_by":"x","ttl_seconds":60}`},
	} {
		got := publicWindowsCall(t, handler, tc.method, "/api/windows", tc.body)
		if got.Code != http.StatusServiceUnavailable || strings.Contains(got.Body.String(), "redis.internal.example") {
			t.Errorf("%s = %d %s, want 503 in fixed words", tc.method, got.Code, got.Body.String())
		}
	}
	if NewPublicWindowsHandler(nil, nil) != nil {
		t.Error("a nil store must leave the route unserved")
	}
}
