package legacyoutput

import (
	"context"
	"encoding/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/go-redis/redis/v8"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"
)

type snapshotRecorder struct {
	batches   int
	snapshots []Snapshot
	err       error
}

func (s *snapshotRecorder) SaveBatch(_ context.Context, snapshots []Snapshot) error {
	s.batches++
	s.snapshots = append(s.snapshots, snapshots...)
	return s.err
}

func TestLocalConverterMatchesPythonAdapter(t *testing.T) {
	raw, err := os.ReadFile("../kafka/testdata/python-legacy-adapter.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		PythonTime int64 `json:"python_time"`
		Params     struct {
			Tenant     string                     `json:"bk_tenant_id"`
			Biz        int64                      `json:"bk_biz_id"`
			Strategies map[string]json.RawMessage `json:"strategies"`
			Events     []struct {
				ID         string                     `json:"event_id"`
				Key        string                     `json:"strategy_key"`
				Kind       string                     `json:"event_kind"`
				Level      uint32                     `json:"primary_level_id"`
				Item       int64                      `json:"item_id"`
				Time       int64                      `json:"source_time"`
				Times      []int64                    `json:"anomaly_timestamps"`
				Dimensions map[string]json.RawMessage `json:"dimensions"`
				Fields     []string                   `json:"dimension_fields"`
				Values     map[string]json.RawMessage `json:"values"`
				Value      json.RawMessage            `json:"value"`
			} `json:"events"`
		} `json:"params"`
		Response struct {
			Events []struct {
				Payload string `json:"payload_json"`
				MD5     string `json:"dedupe_md5"`
			} `json:"events"`
		} `json:"response"`
	}
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	var events []contract.TriggerEventV1
	for _, item := range fixture.Params.Events {
		item.Values["value"] = item.Value
		var strategy struct {
			ID int64 `json:"id"`
		}
		json.Unmarshal(fixture.Params.Strategies[item.Key], &strategy)
		events = append(events, contract.TriggerEventV1{EventID: item.ID, TenantID: fixture.Params.Tenant, BusinessID: strconv.FormatInt(fixture.Params.Biz, 10), PlanRef: contract.RuntimePlanRefV1{StrategyID: strconv.FormatInt(strategy.ID, 10)}, EventKind: item.Kind, PrimaryLevelID: item.Level, RecordRef: contract.TriggerRecordRefV1{SourceTime: item.Time, Dimensions: item.Dimensions}, Observed: contract.TriggerObservedV1{Values: item.Values}, LegacyOutput: &contract.LegacyEventContext{Configuration: contract.FreezeLegacyOutput(&contract.LegacyOutputContext{Strategy: fixture.Params.Strategies[item.Key], DimensionFields: item.Fields, ItemID: strconv.FormatInt(item.Item, 10)}), AnomalyTimestamps: item.Times}})
	}
	store := &snapshotRecorder{}
	converter := Converter{Store: store, Now: func() time.Time { return time.Unix(fixture.PythonTime, 0) }}
	got, err := converter.ConvertBatch(context.Background(), events)
	if err != nil {
		t.Fatal(err)
	}
	if store.batches != 1 || len(store.snapshots) != 1 {
		t.Fatal("snapshot writes were not deduplicated per batch")
	}
	for i, event := range got {
		var actual, expected map[string]any
		json.Unmarshal(event.Payload, &actual)
		json.Unmarshal([]byte(fixture.Response.Events[i].Payload), &expected)
		// UQ's canonical primary field is named value; retain it in original values.
		expected["extra_info"].(map[string]any)["origin_alarm"].(map[string]any)["data"].(map[string]any)["values"].(map[string]any)["value"] = expected["extra_info"].(map[string]any)["origin_alarm"].(map[string]any)["data"].(map[string]any)["value"]
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("local Python protocol differs:\ngot %s\nwant %s", event.Payload, fixture.Response.Events[i].Payload)
		}
		if event.DedupeMD5 != fixture.Response.Events[i].MD5 {
			t.Fatal("Python dedupe bytes differ")
		}
	}
}

func TestServiceSnapshotRoutesStrictUpperBoundAndTTL(t *testing.T) {
	a := startSnapshotRedis(t)
	b := startSnapshotRedis(t)
	store := RedisSnapshotStore{Nodes: map[string]redis.Cmdable{"a": a, "b": b}, Routes: []ServiceRoute{{10, "a"}, {20, "b"}}}
	if err := store.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveBatch(context.Background(), []Snapshot{{9, "prefix.cache.strategy.snapshot.9.1", json.RawMessage(`{"id":9}`)}, {10, "prefix.cache.strategy.snapshot.10.1", json.RawMessage(`{"id":10}`)}}); err != nil {
		t.Fatal(err)
	}
	if a.Exists(context.Background(), "prefix.cache.strategy.snapshot.9.1").Val() != 1 || b.Exists(context.Background(), "prefix.cache.strategy.snapshot.10.1").Val() != 1 || a.TTL(context.Background(), "prefix.cache.strategy.snapshot.9.1").Val() < 3599*time.Second {
		t.Fatal("Python route/TTL mismatch")
	}
	if err := store.SaveBatch(context.Background(), []Snapshot{{20, "bad", json.RawMessage(`{}`)}}); err == nil {
		t.Fatal("out-of-range strategy fell back")
	}
}

func startSnapshotRedis(t *testing.T) *redis.Client {
	t.Helper()
	binary, err := exec.LookPath("redis-server")
	if err != nil {
		t.Skip("redis-server unavailable")
	}
	dir, err := os.MkdirTemp("/tmp", "als-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "redis.sock")
	command := exec.Command(binary, "--port", "0", "--unixsocket", socket, "--save", "", "--appendonly", "no")
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { command.Process.Kill(); command.Wait() })
	client := redis.NewClient(&redis.Options{Network: "unix", Addr: socket})
	t.Cleanup(func() { client.Close() })
	for i := 0; i < 100; i++ {
		if client.Ping(context.Background()).Err() == nil {
			return client
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("redis-server did not start")
	return nil
}
