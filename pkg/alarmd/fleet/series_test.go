// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package fleet

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"
)

type stubRange struct {
	byExpression map[string]SeriesRange
	errFor       string
	windows      []time.Duration
	steps        []time.Duration
}

func (stub *stubRange) Range(
	_ context.Context, promQL string, start, end time.Time, step time.Duration,
) (SeriesRange, error) {
	stub.windows = append(stub.windows, end.Sub(start))
	stub.steps = append(stub.steps, step)
	if promQL == stub.errFor {
		return SeriesRange{}, errors.New("provider is down")
	}
	return stub.byExpression[promQL], nil
}

func seriesHandler(t *testing.T, provider RangeProvider) http.Handler {
	t.Helper()
	service := mustService(t, stubExpectations{expectation: Expectation{QueryGroups: 949, Known: true}},
		stubRegistry{replicas: replicas()}, stubSnapshots{snapshots: healthySnapshots()})
	handler, err := NewHandler(service, nil, func() time.Time { return now }, 0, provider, nil)
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func seriesByKey(t *testing.T, body map[string]any) map[string]map[string]any {
	t.Helper()
	byKey := map[string]map[string]any{}
	for _, raw := range body["series"].([]any) {
		item := raw.(map[string]any)
		byKey[item["key"].(string)] = item
	}
	return byKey
}

// A number on a page is only as trustworthy as the reader's ability to find out
// where it came from, so the expression travels with the curve.
func TestSeriesCarryTheExpressionThatProducedThem(t *testing.T) {
	provider := &stubRange{byExpression: map[string]SeriesRange{
		`max(bkmonitor_alarmd_fleet_objects{state="expected"})`: {
			Points: []SeriesPoint{{AtUnixMilli: 1789006260000, Value: 931}},
		},
	}}
	status, body := get(t, seriesHandler(t, provider), "/api/series")
	if status != http.StatusOK {
		t.Fatalf("status = %d", status)
	}
	byKey := seriesByKey(t, body)
	expected := byKey["expected"]
	if expected["promql"] != `max(bkmonitor_alarmd_fleet_objects{state="expected"})` {
		t.Fatalf("expression not carried: %v", expected["promql"])
	}
	if len(expected["points"].([]any)) != 1 {
		t.Fatalf("points = %v", expected["points"])
	}
	if expected["help"] == "" {
		t.Fatal("a curve without a reading is a decoration")
	}
}

// One provider failure must cost one curve. Failing the whole response would
// take the working curves down with it, exactly when someone came to look.
func TestOneBrokenCurveDoesNotTakeTheOthersDown(t *testing.T) {
	provider := &stubRange{
		errFor: `sum(max by (kind) (bkmonitor_alarmd_fleet_anomalies))`,
		byExpression: map[string]SeriesRange{
			`max(bkmonitor_alarmd_fleet_objects{state="expected"})`: {
				Points: []SeriesPoint{{AtUnixMilli: 1789006260000, Value: 931}},
			},
		},
	}
	_, body := get(t, seriesHandler(t, provider), "/api/series")
	byKey := seriesByKey(t, body)
	if byKey["anomalies"]["unavailable"] != "PROVIDER_ERROR" {
		t.Fatalf("broken curve = %v", byKey["anomalies"])
	}
	if len(byKey["expected"]["points"].([]any)) != 1 {
		t.Fatalf("a working curve was lost with the broken one: %v", byKey["expected"])
	}
}

// The provider reports a missing scope or a missing metric as HTTP 200 with an
// empty series list. Merging that into "no points" makes a misconfigured
// deployment look like a calm one.
func TestARefusedCurveIsNotShownAsAnEmptyOne(t *testing.T) {
	provider := &stubRange{byExpression: map[string]SeriesRange{
		`max(bkmonitor_alarmd_fleet_stalled_objects)`: {
			Code: "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS", Message: "field is empty",
		},
	}}
	_, body := get(t, seriesHandler(t, provider), "/api/series")
	byKey := seriesByKey(t, body)
	if byKey["stalled"]["unavailable"] != "SPACE_TABLE_ID_FIELD_IS_NOT_EXISTS" {
		t.Fatalf("refusal was read as silence: %v", byKey["stalled"])
	}
	// A window that really was empty stays empty rather than being dressed up.
	if byKey["unknown"]["unavailable"] != nil {
		t.Fatalf("an empty window was reported as a refusal: %v", byKey["unknown"])
	}
}

// The window is chosen from a fixed list: a page that can name its own range
// can also name one nobody budgeted for.
func TestTheWindowComesFromAFixedList(t *testing.T) {
	provider := &stubRange{}
	handler := seriesHandler(t, provider)

	_, body := get(t, handler, "/api/series?window=6h")
	if body["window"] != "6h" {
		t.Fatalf("window = %v", body["window"])
	}
	if provider.windows[0] != 6*time.Hour || provider.steps[0] != 5*time.Minute {
		t.Fatalf("window/step = %v/%v", provider.windows[0], provider.steps[0])
	}

	status, _ := get(t, handler, "/api/series?window=90d")
	if status != http.StatusBadRequest {
		t.Fatalf("an unlisted window returned %d", status)
	}
}

// Every metric these curves read is the deployment-wide judgment, and every
// replica exports all of it rather than its own share. So the aggregation that
// touches the raw metric has to be one that collapses the replicas; adding up
// first multiplies a real number by the replica count.
//
// That is invisible on the page. The curve has the right shape, moves when the
// deployment moves, and is simply twice the truth on two replicas -- which also
// means it silently changes when the deployment is scaled, with no code change
// and nothing to notice. The anomaly curve shipped that way.
func TestEveryCurveCollapsesTheReplicasBeforeAddingAnythingUp(t *testing.T) {
	// Captures the aggregation applied directly to the metric selector, which is
	// the one that decides whether the replicas were collapsed. An outer sum over
	// an inner max is fine; an outer sum over the selector itself is the defect.
	innermost := regexp.MustCompile(`([a-z_]+)\s*(?:by\s*\([^)]*\)\s*)?\(\s*(bkmonitor_alarmd_[a-z_]+)`)
	checked := 0
	for _, definition := range seriesCatalog {
		matches := innermost.FindAllStringSubmatch(definition.PromQL, -1)
		if len(matches) == 0 {
			t.Errorf("curve %q reads no alarmd metric through an aggregation: %s", definition.Key, definition.PromQL)
			continue
		}
		for _, match := range matches {
			checked++
			if match[1] != "max" {
				t.Errorf("curve %q applies %s directly to %s: every replica exports the whole deployment's "+
					"number, so this reports it multiplied by the replica count",
					definition.Key, match[1], match[2])
			}
		}
	}
	if checked == 0 {
		t.Fatal("no expression was checked; the guard would pass vacuously")
	}
}

// A deployment that was never told where its own metrics live cannot draw
// curves. An unmounted route says so; a mounted one that always answers with
// nothing does not.
func TestWithoutAProviderTheSeriesRouteIsAbsent(t *testing.T) {
	response := httptest.NewRecorder()
	seriesHandler(t, nil).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/series", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want the route to be absent", response.Code)
	}
}
