package legacyoutput

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"testing"

	"github.com/go-redis/redis/v8"
)

type fixedPod struct{ result *PodMetadata }

func (p fixedPod) LookupPod(context.Context, PodLookup) (*PodMetadata, error) { return p.result, nil }

func TestTargetPythonSourceOracle(t *testing.T) {
	raw, err := os.ReadFile("testdata/python-target.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name       string                     `json:"name"`
		Strategy   json.RawMessage            `json:"strategy"`
		Dimensions map[string]json.RawMessage `json:"dimensions"`
		Fields     []string                   `json:"dimension_fields"`
		Pod        *PodMetadata               `json:"pod_metadata"`
		Expected   TargetProjection           `json:"expected"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	// Cases where alarmd deliberately answers otherwise than Python, with what
	// it answers instead. apm_table: Python reads the APM application out of
	// the result table's name, which is the application's normalised form
	// (hyphens and dots to underscores, lowercased) and so names no
	// application when it had either; alarmd places the record as any other.
	diverges := map[string]TargetProjection{
		"apm_table": {Type: "", Target: nil, Dimensions: map[string]json.RawMessage{"service_name": json.RawMessage(`"svc"`)}, AdditionalDimensions: map[string]json.RawMessage{}},
	}
	for _, tc := range cases {
		if want, ok := diverges[tc.Name]; ok {
			tc.Expected = want
		}
		t.Run(tc.Name, func(t *testing.T) {
			got, err := ProjectTarget(context.Background(), TargetScope{TenantID: "default", BusinessID: 2}, tc.Strategy, tc.Dimensions, tc.Fields, fixedPod{tc.Pod})
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(normalizedJSON(t, got), normalizedJSON(t, tc.Expected)) {
				t.Fatalf("target differs from real Python methods: got=%s want=%s", mustJSON(got), mustJSON(tc.Expected))
			}
		})
	}
}

// The APM application comes from the record's app_name, else the strategy's
// APM-APP label, and never from the result table. A strategy shaped like
// the one that showed it (only service_name in the record, an application
// whose name has hyphens, its table named in normalised form) gets no APM
// object and no app_name, so downstream enrichment falls back to the
// strategy's own data source instead of a name that matches nothing.
func TestTheAPMApplicationIsNeverReadFromTheResultTable(t *testing.T) {
	strategy := func(labels ...string) json.RawMessage {
		raw, _ := json.Marshal(map[string]any{"bk_biz_id": 2, "labels": labels, "items": []any{map[string]any{"query_configs": []any{map[string]any{
			"agg_dimension": []string{"service_name"}, "data_type_label": "time_series", "data_source_label": "bk_monitor",
			"result_table_id": "2_bkapm_metric_cross_bank_trade.__default__"}}}}})
		return raw
	}
	project := func(t *testing.T, s json.RawMessage, dims map[string]json.RawMessage, fields []string) TargetProjection {
		t.Helper()
		got, err := ProjectTarget(context.Background(), TargetScope{TenantID: "default", BusinessID: 2}, s, dims, fields, fixedPod{})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	service := map[string]json.RawMessage{"service_name": json.RawMessage(`"transfer"`)}
	if got := project(t, strategy(), service, []string{"service_name"}); got.Type != "" || got.AdditionalDimensions["app_name"] != nil {
		t.Errorf("no dimension, no label: %s, want no APM object and no app_name", mustJSON(got))
	}
	if got := project(t, strategy("APM-APP(cross-bank-trade)"), service, []string{"service_name"}); got.Type != "APM-SERVICE" ||
		string(got.Target) != `"cross-bank-trade:transfer"` || string(got.AdditionalDimensions["app_name"]) != `"cross-bank-trade"` {
		t.Errorf("label: %s, want the labelled application", mustJSON(got))
	}
	// The application the APM query carries beside the identity dimensions
	// names the object, before any label, and is reported as supplementary.
	carried := map[string]json.RawMessage{"app_name": json.RawMessage(`"cross-bank-trade"`), "service_name": json.RawMessage(`"transfer"`)}
	if got := project(t, strategy("APM-APP(other)"), carried, []string{"service_name"}); string(got.Target) != `"cross-bank-trade:transfer"` ||
		string(got.AdditionalDimensions["app_name"]) != `"cross-bank-trade"` {
		t.Errorf("carried application: %s, want it to name the object", mustJSON(got))
	}
	both := map[string]json.RawMessage{"app_name": json.RawMessage(`"cross-bank-trade"`), "service_name": json.RawMessage(`"transfer"`)}
	if got := project(t, strategy("APM-APP(other)"), both, []string{"app_name", "service_name"}); string(got.Target) != `"cross-bank-trade:transfer"` || got.AdditionalDimensions["app_name"] != nil {
		t.Errorf("dimension and label: %s, want the dimension's application, not repeated", mustJSON(got))
	}
}

func mustJSON(v any) []byte { raw, _ := json.Marshal(v); return raw }
func normalizedJSON(t *testing.T, v any) any {
	t.Helper()
	var out any
	if err := json.Unmarshal(mustJSON(v), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

type podCacheStub struct {
	value string
	err   error
	keys  []string
}

func (s *podCacheStub) Get(_ context.Context, key string) *redis.StringCmd {
	s.keys = append(s.keys, key)
	return redis.NewStringResult(s.value, s.err)
}

func TestDjangoPodCachePythonWire(t *testing.T) {
	raw, err := os.ReadFile("testdata/python-pod-cache.json")
	if err != nil {
		t.Fatal(err)
	}
	var cases []struct {
		Name string `json:"name"`
		Key  string `json:"key"`
		Wire string `json:"wire_base64"`
	}
	if err := json.Unmarshal(raw, &cases); err != nil {
		t.Fatal(err)
	}
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			wire, err := base64.StdEncoding.DecodeString(tc.Wire)
			if err != nil {
				t.Fatal(err)
			}
			client := &podCacheStub{value: string(wire)}
			var outcomes []string
			resolver, err := NewDjangoPodResolver(client, PodCacheConfig{KeyPrefix: "prefix", Version: 1, Observe: func(v string) { outcomes = append(outcomes, v) }})
			if err != nil {
				t.Fatal(err)
			}
			ns := "ns"
			for _, business := range []int64{2, -2} {
				got, err := resolver.LookupPod(context.Background(), PodLookup{Scope: TargetScope{TenantID: "default", BusinessID: business}, ClusterID: "cluster", Namespace: &ns, Name: "pod"})
				if err != nil || got == nil || *got != (PodMetadata{Namespace: "ns", WorkloadType: "Deployment", WorkloadName: "web"}) {
					t.Fatalf("Python cache hit lost for business %d: got=%+v err=%v", business, got, err)
				}
			}
			if !reflect.DeepEqual(client.keys, []string{tc.Key, tc.Key}) || !reflect.DeepEqual(outcomes, []string{"hit", "hit"}) {
				t.Fatalf("wrong key/outcome: %v %v", client.keys, outcomes)
			}
		})
	}
}

func TestDjangoPodCacheMissAndInvalidAreObservableFallbacks(t *testing.T) {
	for _, tc := range []struct {
		name, wire string
		err        error
		want       string
	}{
		{name: "miss", err: redis.Nil, want: "miss"},
		{name: "unavailable", err: errors.New("offline"), want: "error"},
		{name: "not_pickle", wire: `{"namespace":"ns"}`, want: "error"},
		{name: "object_pickle", wire: "\x80\x04cos\nsystem\n.", want: "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var outcome string
			client := &podCacheStub{value: tc.wire, err: tc.err}
			resolver, _ := NewDjangoPodResolver(client, PodCacheConfig{Version: 1, Observe: func(v string) { outcome = v }})
			got, err := resolver.LookupPod(context.Background(), PodLookup{Scope: TargetScope{TenantID: "default", BusinessID: 2}, ClusterID: "cluster", Name: "pod"})
			if got != nil || err != nil || outcome != tc.want || client.keys[0] != ":1:bcs_pod:cluster:None:pod" {
				t.Fatalf("fallback: got=%v err=%v outcome=%s keys=%v", got, err, outcome, client.keys)
			}
		})
	}
}

func TestDjangoPodCacheUsesConfiguredConnectionWithoutTenantKey(t *testing.T) {
	client := &podCacheStub{}
	resolver, _ := NewDjangoPodResolver(client, PodCacheConfig{Version: 1})
	got, err := resolver.LookupPod(context.Background(), PodLookup{Scope: TargetScope{TenantID: "other"}, ClusterID: "cluster", Name: "pod"})
	if got != nil || err != nil || len(client.keys) != 1 {
		t.Fatal("Django key must use configured connection without an invented tenant prefix")
	}
}

func TestPodBatchMemoUsesNamespaceValueAndExpiresWithBatch(t *testing.T) {
	client := &podCacheStub{err: redis.Nil}
	resolver, _ := NewDjangoPodResolver(client, PodCacheConfig{Version: 1})
	first := NewBatchPodResolver(resolver)
	lookup := PodLookup{Scope: TargetScope{TenantID: "default", BusinessID: 2}, ClusterID: "cluster", Name: "pod"}
	for i := 0; i < 3; i++ {
		ns := "ns"
		lookup.Namespace = &ns
		if _, err := first.LookupPod(context.Background(), lookup); err != nil {
			t.Fatal(err)
		}
	}
	if len(client.keys) != 1 {
		t.Fatalf("same pod queried %d times", len(client.keys))
	}
	if _, err := NewBatchPodResolver(resolver).LookupPod(context.Background(), lookup); err != nil {
		t.Fatal(err)
	}
	if len(client.keys) != 2 {
		t.Fatal("batch memo escaped its batch")
	}
}
