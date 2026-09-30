package uq

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

func diagnosticSeries(ip string, points int) string {
	values := make([]string, points)
	for i := range values {
		values[i] = fmt.Sprintf("[%d,%d]", 1700123456000+int64(i)*1000, i+1)
	}
	return `{"name":"a","columns":["_time","_result"],"types":["int64","float64"],"group_keys":["bk_target_ip_table1"],"group_values":["` + ip + `"],"values":[` + strings.Join(values, ",") + `]}`
}

func diagnosticFixture(t *testing.T, status int, payload string) *DiagnosticClient {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(server.Close)
	client, err := NewDiagnosticClient(server.URL, "alarmd-diagnostic", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	return client
}

type diagnosticRoundTripper func(*http.Request) (*http.Response, error)

func (f diagnosticRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDiagnosticPreviewAndQueryUseProductionWire(t *testing.T) {
	for _, promql := range []bool{false, true} {
		t.Run(fmt.Sprintf("promql_%t", promql), func(t *testing.T) {
			attempt := validAttempt(t)
			if promql {
				facts := attempt.Spec.PlanFacts
				facts.QueryRevision, facts.MetricMerge = "", ""
				facts.QueryList = nil
				facts.PromQL = &execution.PromQLQuery{Expression: `sum(rate(requests{path="/business"}[5m])) or vector(100)`, Match: `{job="fixture"}`}
				facts.Normalization.DatasetContract.IdentityFields = []string{}
				facts.Normalization.DatasetContract.DynamicDimensions = true
				var err error
				facts, err = execution.BuildQueryPlanFacts(facts)
				if err != nil {
					t.Fatal(err)
				}
				spec := attempt.Spec
				spec.Digest, spec.PlanFacts = "", facts
				attempt.Spec, err = execution.BuildPhysicalQuerySpec(spec)
				if err != nil {
					t.Fatal(err)
				}
			}
			type received struct {
				path    string
				body    []byte
				headers http.Header
			}
			var mu sync.Mutex
			var requests []received
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				raw, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, received{r.URL.Path, raw, r.Header.Clone()})
				mu.Unlock()
				_, _ = io.WriteString(w, `{"series":[`+diagnosticSeries("127.0.0.1", 2)+`],"is_partial":false}`)
			}))
			defer server.Close()
			const secret = "fixture-transport-credential"
			transport := server.Client().Transport
			httpClient := &http.Client{Transport: diagnosticRoundTripper(func(r *http.Request) (*http.Response, error) {
				r = r.Clone(r.Context())
				r.Header.Set("Authorization", "Bearer "+secret)
				r.Header.Set("Cookie", secret)
				return transport.RoundTrip(r)
			})}
			client, err := NewDiagnosticClient(server.URL, "alarmd-diagnostic", httpClient)
			if err != nil {
				t.Fatal(err)
			}
			preview, err := client.Preview(attempt.Spec)
			if err != nil {
				t.Fatal(err)
			}
			if len(requests) != 0 {
				t.Fatal("preview sent a provider request")
			}
			previewJSON, _ := json.Marshal(preview)
			if bytes.Contains(previewJSON, []byte(secret)) || bytes.Contains(previewJSON, []byte(server.URL)) || len(preview.Headers) != 4 {
				t.Fatal("preview exposed connection metadata")
			}
			if promql && !bytes.Contains(preview.Body, []byte(`sum(rate(requests`)) || !promql && !bytes.Contains(preview.Body, []byte(`127.0.0.1`)) {
				t.Fatal("preview hid business expression or condition")
			}
			result, err := client.Query(context.Background(), attempt.Spec, DiagnosticSelection{})
			if err != nil || !result.Complete || result.RequestDigest != preview.RequestDigest || result.ReturnedPoints != 2 {
				t.Fatalf("diagnostic: %+v %v", result, err)
			}
			production, _ := NewClient(server.URL, "alarmd-diagnostic", httpClient)
			if _, err := production.Execute(context.Background(), attempt, &collectingSink{}); err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if len(requests) != 2 || requests[0].path != requests[1].path || !bytes.Equal(requests[0].body, requests[1].body) || !bytes.Equal(preview.Body, requests[0].body) || preview.Path != requests[0].path {
				t.Fatal("diagnostic wire differs from production")
			}
			for key, value := range preview.Headers {
				if requests[0].headers.Get(key) != value || requests[1].headers.Get(key) != value {
					t.Fatalf("semantic header differs: %s", key)
				}
			}
			if result.Series[0].Points[0].SourceTime != 1700123456 || string(result.Series[0].Points[0].Value) != "1" {
				t.Fatal("standard source-time/value normalization changed")
			}
		})
	}
}

func TestDiagnosticSelectionDoesNotModifyQueryOrStopTailDecode(t *testing.T) {
	var bodies [][]byte
	var mu sync.Mutex
	payload := `{"series":[` + diagnosticSeries("one", 2) + `,` + diagnosticSeries("two", 2) + `,` + diagnosticSeries("three", 2) + `],"status":{"code":"QUERY_TS_PARTIAL"},"is_partial":true}`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
		_, _ = io.WriteString(w, payload)
	}))
	defer server.Close()
	client, _ := NewDiagnosticClient(server.URL, "alarmd-diagnostic", server.Client())
	spec := validAttempt(t).Spec
	all, err := client.Query(context.Background(), spec, DiagnosticSelection{MaxPoints: 3})
	if err != nil || all.Complete || !all.Truncated || all.ReturnedPoints != 3 || all.Scan.Series != 3 || all.Scan.Records != 6 || !all.Scan.Complete || all.Scan.Bytes != uint64(len(payload)) || all.Completion.Completeness != execution.CompletenessPartial || all.Completion.Status.Code != queryTSPartial {
		t.Fatalf("tail or counters lost: %+v %v", all, err)
	}
	selected, err := client.Query(context.Background(), spec, DiagnosticSelection{SeriesDigest: all.Series[1].SeriesDigest})
	if err != nil || len(selected.Series) != 1 || selected.ReturnedPoints != 2 || selected.Scan.Series != 3 || selected.Scan.Records != 6 || selected.MatchedSeries != 1 || selected.Complete {
		t.Fatalf("selection changed provider work: %+v %v", selected, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || !bytes.Equal(bodies[0], bodies[1]) || all.RequestDigest != selected.RequestDigest {
		t.Fatal("series selection changed original aggregation query")
	}
}

func TestDiagnosticEmptyUnavailableAndMalformedResponsesStayDistinct(t *testing.T) {
	for _, tc := range []struct {
		name, payload string
		status        int
		complete      bool
		completeness  execution.Completeness
		code          string
	}{
		{"empty", `{"series":[],"is_partial":false}`, 200, true, execution.CompletenessFull, ""},
		{"unavailable_tail", `{"series":[` + diagnosticSeries("one", 1) + `],"status":{"code":"SPACE_IS_NOT_EXISTS","message":"fixture-secret"},"is_partial":false}`, 200, false, execution.CompletenessUnavailable, ""},
		{"missing_tail", `{"series":[]}`, 200, false, execution.CompletenessUnavailable, ""},
		{"malformed", `{"series":["fixture-secret"]}`, 200, false, "", "provider_response_invalid"},
		{"http_error", `fixture-secret`, 503, false, execution.CompletenessUnavailable, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := diagnosticFixture(t, tc.status, tc.payload)
			result, err := client.Query(context.Background(), validAttempt(t).Spec, DiagnosticSelection{})
			if tc.code != "" {
				assertDiagnosticCode(t, err, tc.code)
			} else if err != nil {
				t.Fatal(err)
			}
			if result.Complete != tc.complete || (result.Completion != nil && result.Completion.Completeness != tc.completeness) {
				t.Fatalf("wrong completion: %+v", result)
			}
			raw, _ := json.Marshal(result)
			if bytes.Contains(raw, []byte("fixture-secret")) || err != nil && strings.Contains(err.Error(), "fixture-secret") {
				t.Fatal("upstream error text leaked")
			}
			if tc.name == "empty" && (len(result.Series) != 0 || result.Completion.DataState != execution.DataStateEmpty || !result.Scan.Complete) {
				t.Fatal("real empty response is not complete empty")
			}
		})
	}
}

func TestDiagnosticBudgetsAreSeparateFromProductionAndErrorsAreSafe(t *testing.T) {
	want := Limits{MaxBodyBytes: 4 << 20, MaxSeriesBytes: 512 << 10, MaxSeries: 1000, MaxRecords: 20000}
	if DiagnosticLimits() != want || DiagnosticLimits() == DefaultLimits() {
		t.Fatal("diagnostic defaults are not independent")
	}
	for _, tc := range []struct{ name, payload, code string }{
		{"body", `{"ignored":"` + strings.Repeat("x", 4<<20) + `","series":[],"is_partial":false}`, "response_bytes_exceeded"},
		{"series_bytes", `{"series":[{"name":"` + strings.Repeat("x", 512<<10) + `"}],"is_partial":false}`, "series_bytes_exceeded"},
		{"series_count", `{"series":[` + strings.TrimSuffix(strings.Repeat(diagnosticSeries("one", 1)+",", 1001), ",") + `],"is_partial":false}`, "total_series_exceeded"},
		{"record_count", `{"series":[` + diagnosticSeries("one", 20001) + `],"is_partial":false}`, "total_records_exceeded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := diagnosticFixture(t, 200, tc.payload)
			result, err := client.Query(context.Background(), validAttempt(t).Spec, DiagnosticSelection{MaxSeries: 1, MaxPoints: 1})
			assertDiagnosticCode(t, err, tc.code)
			if result.Complete || result.Scan.Complete || result.Completion != nil || !containsDiagnostic(result.Limitations, tc.code) {
				t.Fatal("budget failure was reported as final complete evidence")
			}
		})
	}
}

func TestDiagnosticCancellationConcurrencyAndNoRetry(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		close(started)
		w.WriteHeader(200)
		_, _ = io.WriteString(w, `{"series":[`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()
	client, _ := NewDiagnosticClient(server.URL, "alarmd-diagnostic", server.Client())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	spec := validAttempt(t).Spec
	go func() { _, err := client.Query(ctx, spec, DiagnosticSelection{}); done <- err }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("query did not start")
	}
	_, err := client.Query(context.Background(), spec, DiagnosticSelection{})
	assertDiagnosticCode(t, err, "diagnostic_busy")
	cancel()
	select {
	case err := <-done:
		assertDiagnosticCode(t, err, "query_canceled")
	case <-time.After(time.Second):
		t.Fatal("query did not honor cancellation")
	}
	if calls.Load() != 1 {
		t.Fatal("busy/canceled query retried")
	}
	var failedCalls atomic.Int32
	client, _ = NewDiagnosticClient("http://fixture.invalid", "alarmd-diagnostic", &http.Client{Transport: diagnosticRoundTripper(func(*http.Request) (*http.Response, error) {
		failedCalls.Add(1)
		return nil, errors.New("fixture-secret-url")
	})})
	result, err := client.Query(context.Background(), spec, DiagnosticSelection{})
	if err != nil || result.Completion.Completeness != execution.CompletenessUnavailable || failedCalls.Load() != 1 {
		t.Fatal("transport error retried or lost classification")
	}
	raw, _ := json.Marshal(result)
	if bytes.Contains(raw, []byte("fixture-secret-url")) {
		t.Fatal("transport secret leaked")
	}
}

func TestDiagnosticRejectsRedirectAndInvalidSelectionBeforeQuery(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer source.Close()
	client, _ := NewDiagnosticClient(source.URL, "alarmd-diagnostic", source.Client())
	spec := validAttempt(t).Spec
	result, err := client.Query(context.Background(), spec, DiagnosticSelection{})
	if err != nil || result.Complete || targetCalls.Load() != 0 {
		t.Fatal("diagnostic followed redirect")
	}
	for _, selection := range []DiagnosticSelection{{MaxSeries: 11}, {MaxSeries: -1}, {MaxPoints: 501}, {MaxPoints: -1}, {SeriesDigest: "bad"}} {
		_, err := client.Query(context.Background(), spec, selection)
		assertDiagnosticCode(t, err, "query_selection_invalid")
	}
	for _, endpoint := range []string{"http://user:fixture-secret@example.test", "http://example.test?token=fixture-secret", "file:///tmp/test"} {
		_, err := NewDiagnosticClient(endpoint, "alarmd", http.DefaultClient)
		assertDiagnosticCode(t, err, "diagnostic_config_invalid")
		if strings.Contains(err.Error(), "fixture-secret") {
			t.Fatal("invalid configuration leaked credentials")
		}
	}
}

func TestDiagnosticDeadlineAndMetadataBudget(t *testing.T) {
	var observedDeadline time.Duration
	client, _ := NewDiagnosticClient("http://fixture.invalid", "alarmd-diagnostic", &http.Client{Transport: diagnosticRoundTripper(func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Error("diagnostic transport has no deadline")
		}
		observedDeadline = time.Until(deadline)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})})
	spec := validAttempt(t).Spec
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	_, err := client.Query(ctx, spec, DiagnosticSelection{})
	assertDiagnosticCode(t, err, "query_timeout")
	if observedDeadline > 250*time.Millisecond || observedDeadline <= 0 {
		t.Fatal("caller deadline was extended")
	}
	client = diagnosticFixture(t, 200, `{"series":[],"is_partial":false,"result_table_id":["`+strings.Repeat("x", 32<<10)+`"]}`)
	result, err := client.Query(context.Background(), validAttempt(t).Spec, DiagnosticSelection{})
	if err != nil || !result.Truncated || result.Complete || !result.Scan.Complete || !containsDiagnostic(result.Limitations, "provider_metadata_truncated") {
		t.Fatal("metadata escaped output budget")
	}
}

func TestDiagnosticDefaultOutputAndMissingSelectedSeries(t *testing.T) {
	series := make([]string, 6)
	for i := range series {
		series[i] = diagnosticSeries(fmt.Sprintf("host%d", i), 21)
	}
	client := diagnosticFixture(t, 200, `{"series":[`+strings.Join(series, ",")+`],"is_partial":false}`)
	spec := validAttempt(t).Spec
	result, err := client.Query(context.Background(), spec, DiagnosticSelection{})
	if err != nil || result.ReturnedPoints != 100 || len(result.Series) != 5 || !result.Truncated || result.Complete || result.Scan.Records != 126 {
		t.Fatalf("default output budget changed: %+v %v", result, err)
	}
	missing, err := client.Query(context.Background(), spec, DiagnosticSelection{SeriesDigest: strings.Repeat("0", 64)})
	if err != nil || len(missing.Series) != 0 || missing.MatchedSeries != 0 || !missing.Complete || missing.Scan.Records != 126 || missing.RequestDigest != result.RequestDigest {
		t.Fatal("unmatched selection became empty provider data or changed query")
	}
	if !reflect.DeepEqual(missing.Limitations, []string{}) {
		t.Fatal("complete selected view has spurious limitation")
	}
}

func assertDiagnosticCode(t *testing.T, err error, code string) {
	t.Helper()
	var diagnostic *DiagnosticError
	if !errors.As(err, &diagnostic) || diagnostic.Code != code {
		t.Fatalf("error=%v want code=%s", err, code)
	}
}
func containsDiagnostic(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
