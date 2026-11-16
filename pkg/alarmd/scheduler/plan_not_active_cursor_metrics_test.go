// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// cursorSkipsServed is the applied PLAN_NOT_ACTIVE cell of
// schedule_cursor_advance_total that /metrics serves for inFlight; a cell it
// does not serve fails, since every cell exists from the start.
func cursorSkipsServed(t *testing.T, recorder *metric.Recorder, inFlight string) float64 {
	t.Helper()
	served := httptest.NewRecorder()
	promhttp.HandlerFor(recorder.Gatherer(), promhttp.HandlerOpts{}).ServeHTTP(served, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	want := `alarmd_schedule_cursor_advance_total{in_flight="` + inFlight + `",reason="` + contract.ReasonPlanNotActive + `",refusal="",result="applied"} `
	for _, line := range strings.Split(served.Body.String(), "\n") {
		if strings.HasPrefix(line, want) {
			value, err := strconv.ParseFloat(strings.TrimPrefix(line, want), 64)
			if err != nil {
				t.Fatalf("/metrics line %q: %v", line, err)
			}
			return value
		}
	}
	t.Fatalf("/metrics serves no %s", want)
	return 0
}

// A cursor skip past a Segment with no Plan due is one span of Slots ended
// PLAN_NOT_ACTIVE, counted applied under that word; one that also threw away
// the Slot in flight says so (in_flight="true"), and its slot_skipped line,
// written whatever the log samples, names that Slot, which has no
// slot_completed line of its own.
func TestAnAppliedPlanNotActiveSkipIsCountedAndNamesTheSlotItThrewAway(t *testing.T) {
	for _, inFlight := range []execution.EvaluationTime{0, 120} {
		t.Run("in flight "+strconv.FormatInt(int64(inFlight), 10), func(t *testing.T) {
			recorder := metric.NewRecorder(metric.BuildInfo{})
			limiter, err := observability.NewScopedLogLimiter(observability.ScopedLogLimiterConfig{Window: time.Minute, MaxEvents: 1, MaxScopes: 1024})
			if err != nil {
				t.Fatal(err)
			}
			policy, err := observability.NewScopedBoundedLogPolicy(limiter)
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			observer := observability.Multi(observability.Named(observability.ObserverRecorder, recorder),
				observability.Named(observability.ObserverLogging, observability.NewLoggingObserver(observability.New("alarmd", &output), policy)))
			catalog := &fakeSlotCatalog{t: t,
				schedules: []execution.FrozenQueryGroupSchedule{schedulerSchedule(t, 60, 600, nil, "snapshot-1", 1)}}
			progress := execution.ScheduleProgress{
				Identity: execution.ProgressIdentity{QueryGroup: "query-group-1"}, NextSlot: 120,
				LastFullSlot: 60, LastCompletionKind: execution.CompletionFull,
			}
			reader := &advancingProgressReader{
				fakeProgressReader: &fakeProgressReader{
					result:  execution.ProgressLoadResult{Status: execution.ProgressFound, Progress: &progress},
					catalog: catalog,
				},
				result: execution.ProgressSkipResult{Status: execution.ProgressCommitted, InFlightSlot: inFlight},
			}
			source := prunedCursorSource(t, catalog, reader, time.Unix(661, 0), observer)
			if _, advanced, err := source.advancePrunedCursor(context.Background(), progress, testFence(7), &SourceBlockedError{Err: ErrNoPlanDueInSegment}); err != nil || !advanced {
				t.Fatalf("advancePrunedCursor() = (advanced %v, error %v), want the cursor moved", advanced, err)
			}
			threwAway, kept := 0.0, 1.0
			if inFlight != 0 {
				threwAway, kept = 1, 0
			}
			if got := cursorSkipsServed(t, recorder, "true"); got != threwAway {
				t.Fatalf("schedule_cursor_advance_total{applied,PLAN_NOT_ACTIVE,in_flight=true} = %v, want %v", got, threwAway)
			}
			if got := cursorSkipsServed(t, recorder, "false"); got != kept {
				t.Fatalf("schedule_cursor_advance_total{applied,PLAN_NOT_ACTIVE,in_flight=false} = %v, want %v", got, kept)
			}
			var skipped []map[string]any
			for _, raw := range strings.Split(strings.TrimSpace(output.String()), "\n") {
				var line map[string]any
				if err := json.Unmarshal([]byte(raw), &line); err != nil {
					t.Fatalf("log line is not JSON: %v: %q", err, raw)
				}
				if line["stage"] == "slot_skipped" {
					skipped = append(skipped, line)
				}
			}
			if len(skipped) != 1 || skipped[0]["skip_reason"] != contract.ReasonPlanNotActive {
				t.Fatalf("slot_skipped lines %v, want the one skip under PLAN_NOT_ACTIVE", skipped)
			}
			named, present := skipped[0]["in_flight_slot"]
			if inFlight == 0 && present || inFlight != 0 && named != float64(inFlight) {
				t.Fatalf("slot_skipped in_flight_slot = %v (present %v), want %d named only when it was thrown away", named, present, inFlight)
			}
		})
	}
}
