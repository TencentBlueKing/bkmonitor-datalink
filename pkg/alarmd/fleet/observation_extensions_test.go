package fleet

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/controlplane"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

func TestObservationCostHTTPReadsOnlyCachedPayloadAndRejectsStale(t *testing.T) {
	at := time.Now()
	cache := NewCostCandidatesCache(func() time.Time { return at }, time.Minute)
	h := WithCostCandidates(http.NotFoundHandler(), cache)
	request := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/objects?scope=cost", nil))
		return w
	}
	if w := request(); w.Code != 503 {
		t.Fatalf("unobserved cost %d", w.Code)
	}
	cache.Update(CostCandidatesSnapshot{ObservedAt: at, Projection: CostProjectionView{Snapshots: []CostProjectionRecord{{Replica: "worker", Cost: json.RawMessage(`{"process_id":"process","enabled":true}`)}}}})
	first := request()
	if first.Code != 200 {
		t.Fatalf("cache=%d", first.Code)
	}
	for i := 0; i < 100; i++ {
		if w := request(); w.Body.String() != first.Body.String() {
			t.Fatal("HTTP recomputed snapshot")
		}
	}
	at = at.Add(2 * time.Minute)
	if w := request(); w.Code != 503 || !strings.Contains(w.Body.String(), "COST_SNAPSHOT_STALE") {
		t.Fatalf("stale %d %s", w.Code, w.Body.String())
	}
}

func TestObservationSampleAPIPreservesLegacyAndExposesExtraBudget(t *testing.T) {
	s, _ := fleetSampleFixture(t)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) })
	h := WithSeriesSamples(next, nil, nil, nil, nil, s, time.Now)
	for _, path := range []string{"/api/objects/group?records=2", "/api/objects/group?check=COMPLETENESS&group=source"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 202 {
			t.Fatalf("legacy changed %s: %d", path, w.Code)
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/windows", strings.NewReader(`{"query_groups":["group"]}`)))
	if w.Code != 202 {
		t.Fatalf("legacy window changed %d", w.Code)
	}
	for _, p := range []string{"records=1", "check=A", "group=B"} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", "/api/objects/group?samples=1&"+p, nil))
		if w.Code != 400 {
			t.Fatalf("sample silently replaces detail: %d", w.Code)
		}
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/windows?mode=sample", nil))
	var result struct {
		Enabled bool `json:"enabled"`
		Budget  struct {
			Bytes  int                              `json:"combined_bytes_per_minute"`
			Limits observability.SeriesSampleLimits `json:"sample_extra_allocation"`
		} `json:"budget"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Enabled || result.Budget.Bytes != observability.TargetFlowMaxBytes+s.Limits().BytesPerMinute || result.Budget.Limits != s.Limits() {
		t.Fatalf("budget projection %+v", result)
	}
}

func TestObservationUnavailableDirectoryDoesNotDelegateToAnomalyList(t *testing.T) {
	h := WithStrategyDirectory(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("strategy request became anomaly list") }), nil, nil, nil, "", time.Now)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/objects?scope=strategies", nil))
	if w.Code != 503 {
		t.Fatalf("missing directory=%d", w.Code)
	}
}

// unreadyDirectory is a process holding no publication of its own.
type unreadyDirectory struct{}

func (unreadyDirectory) Available() bool { return false }
func (unreadyDirectory) Page(context.Context, time.Time, string, string, string, int, int) controlplane.StrategyDirectorySnapshot {
	return controlplane.StrategyDirectorySnapshot{Reason: "LEADER_CATALOG_NOT_READY", Rows: []controlplane.StrategyDirectoryRow{}}
}
func (unreadyDirectory) ResolveCurrent(context.Context, time.Time, string, string, string, string, ...string) (controlplane.StrategyDirectoryRow, error) {
	return controlplane.StrategyDirectoryRow{}, controlplane.ErrSnapshotUnavailable
}
func (unreadyDirectory) EffectivePlan(context.Context, controlplane.StrategyDirectoryRow) (controlplane.QueryGroupPlanObject, error) {
	return controlplane.QueryGroupPlanObject{}, controlplane.ErrSnapshotUnavailable
}
func (unreadyDirectory) EffectiveOutput(context.Context, controlplane.StrategyDirectoryRow) controlplane.OutputFormatFacts {
	return controlplane.OutputFormatFacts{}
}

// A sample window names a strategy's current row, which only the Leader's
// directory resolves: a follower hands the open to the Leader with its
// method and its body as they came, and one already handed on is answered
// where it lands.
func TestAFollowerHandsASampleWindowOpenToTheLeaderWithItsBody(t *testing.T) {
	s, _ := fleetSampleFixture(t)
	client := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = client.Close() })
	windows, err := NewWindowStore(client, "alarmd:test:windows")
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewDiagnosticStore(client, "alarmd:test:fleet")
	if err != nil {
		t.Fatal(err)
	}
	var methods, bodies []string
	forward := func(w http.ResponseWriter, r *http.Request) (bool, string) {
		payload, _ := io.ReadAll(r.Body)
		methods, bodies = append(methods, r.Method), append(bodies, string(payload))
		writeJSON(w, http.StatusOK, map[string]string{"answered_by": "leader"})
		return true, ""
	}
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("a sample open reached the plain window route") })
	h := WithSeriesSamples(next, unreadyDirectory{}, forward, windows, store, s, time.Now)
	open := `{"mode":"sample","strategy":"1001","series_digest":"series","ttl_seconds":60,"opened_by":"test"}`
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("POST", "/api/windows", strings.NewReader(open)))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "leader") || len(methods) != 1 || methods[0] != "POST" || bodies[0] != open {
		t.Fatalf("follower open = %d %s, hops %v %q; want the Leader's answer to the same POST and body", w.Code, w.Body.String(), methods, bodies)
	}
	w = httptest.NewRecorder()
	handedOn := httptest.NewRequest("POST", "/api/windows", strings.NewReader(open))
	handedOn.Header.Set(ForwardedHeader(), "another")
	h.ServeHTTP(w, handedOn)
	if w.Code != http.StatusServiceUnavailable || !strings.Contains(w.Body.String(), "STRATEGY_SELECTION_UNKNOWN") || len(methods) != 1 {
		t.Fatalf("handed-on open on a follower = %d %s after %d hops, want refused where it landed", w.Code, w.Body.String(), len(methods))
	}
}
