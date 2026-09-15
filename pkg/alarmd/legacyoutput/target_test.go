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
	for _, tc := range cases {
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
