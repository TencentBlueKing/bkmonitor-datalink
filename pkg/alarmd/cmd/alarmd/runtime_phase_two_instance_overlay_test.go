// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
)

// The service instance the case's series names: instance 7 of host 720002,
// 192.0.2.172 in cloud 0, as the cache writer copies them from the host.
const overlayInstanceRecord = `{"service_instance_id":7,"bk_host_id":720002,"ip":"192.0.2.172","bk_cloud_id":0,` +
	`"topo_link":{"module|91":[{"bk_obj_id":"module","bk_inst_id":91}]}}`

// startInstanceFixture is the lifecycle fixture with the instance in CMDB and
// one series that names it, reporting another address, over the threshold;
// the item aggregated by groupBy.
func startInstanceFixture(t *testing.T, groupBy []string) *lifecycleFixture {
	t.Helper()
	address, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	prefix := validGoAccessRuntimeConfig().PlatformKeyPrefix()
	for _, instances := range []string{prefix + ".cache.cmdb.service_instance", "tenant-a." + prefix + ".cache.cmdb.service_instance"} {
		if err := client.HSet(ctx, instances, "7", overlayInstanceRecord).Err(); err != nil {
			t.Fatal(err)
		}
	}
	strategy := lifecycleStrategy{revision: 7, noDataOff: true, edit: func(item map[string]any) {
		dimensions := make([]any, 0, len(groupBy))
		for _, name := range groupBy {
			dimensions = append(dimensions, name)
		}
		item["query_configs"].([]any)[0].(map[string]any)["agg_dimension"] = dimensions
	}}
	fixture := startLifecycleFixtureOn(t, address, client, strategy, overlayHostRecords(), nil)
	values := map[string]string{"bk_target_service_instance_id": "7", "bk_target_ip": "198.51.100.9", "bk_target_cloud_id": "0"}
	fixture.interceptQueries(func(writer http.ResponseWriter, request *http.Request) bool {
		writer.Header().Set("Content-Type", "application/json")
		var payload struct {
			EndTime string `json:"end_time"`
		}
		_ = json.NewDecoder(request.Body).Decode(&payload)
		end, err := strconv.ParseInt(payload.EndTime, 10, 64)
		if err != nil || end <= 0 {
			end = fixture.clock.Load() / 1000
		}
		if end > 1_000_000_000_000 {
			end /= 1000
		}
		keys, groupValues := make([]string, 0, len(groupBy)), make([]string, 0, len(groupBy))
		for _, name := range groupBy {
			keys, groupValues = append(keys, `"`+name+`"`), append(groupValues, `"`+values[name]+`"`)
		}
		_, _ = writer.Write([]byte(`{"series":[{"name":"_result0","columns":["_time","_result"],"types":["int64","float64"],` +
			`"group_keys":[` + strings.Join(keys, ",") + `],"group_values":[` + strings.Join(groupValues, ",") + `],` +
			`"values":[[` + strconv.FormatInt((end-1)*1000, 10) + `,90]]}],"status":null,"trace_id":"instance-overlay",` +
			`"is_partial":false,"result_table_id":["system.cpu"]}`))
		return true
	})
	return fixture
}

func instanceFingerprint(t *testing.T, groupBy []string, dimensions map[string]json.RawMessage) string {
	t.Helper()
	value, err := contract.MonitorDedupeMD5(lifecycleStrategyID, "2", dimensions, contract.MonitorOutputIdentity{DimensionFields: groupBy})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

// A series that names its service instance and reports another address than
// the instance's host, aggregated by bk_target_ip: Python's fuller writes the
// instance's address and cloud into the record, so its alert target is that
// host. Through the production bundle, the alert opens under the instance's
// address, and the alert the consumer holds under the record's own - opened
// before this - is closed as superseded_identity, not recovered.
func TestAnInstancePlacedAlertIsFiledUnderTheInstancesAddress(t *testing.T) {
	groupBy := []string{"bk_target_service_instance_id", "bk_target_ip", "bk_target_cloud_id"}
	own := map[string]json.RawMessage{"bk_target_service_instance_id": json.RawMessage(`"7"`),
		"bk_target_ip": json.RawMessage(`"198.51.100.9"`), "bk_target_cloud_id": json.RawMessage(`"0"`)}
	python := map[string]json.RawMessage{"bk_target_service_instance_id": json.RawMessage(`"7"`),
		"bk_target_ip": json.RawMessage(`"192.0.2.172"`), "bk_target_cloud_id": json.RawMessage(`0`)}
	old, current := instanceFingerprint(t, groupBy, own), instanceFingerprint(t, groupBy, python)
	if old == current {
		t.Fatal("the fixture's fingerprints do not move as the case needs")
	}
	fixture := startInstanceFixture(t, groupBy)
	closer := fixture.bundle.targetScopeClose
	if closer == nil {
		t.Fatal("the fixture's bundle has no fact-close")
	}
	for _, held := range []string{old, current} {
		fixture.publishOpenAlert(held)
	}
	step := func(round int64) {
		fixture.mustAttempt(round)
		closer.Step(context.Background())
	}
	step(1)
	fixture.waitOpenAlertSetRead(1)
	step(2)
	step(3)
	fixture.sink.mu.Lock()
	written := append([]contract.TriggerEventV1(nil), fixture.sink.written...)
	fixture.sink.mu.Unlock()
	closes := fixture.linkCloses()
	if len(written) == 0 {
		t.Fatal("no event was written")
	}
	for _, event := range written {
		if event.DedupeMD5 != current {
			t.Fatalf("%s filed under %s, want the instance address's %s, never the record's %s", event.EventKind, event.DedupeMD5, current, old)
		}
		if got := string(event.RecordRef.Dimensions["bk_target_ip"]); got != `"192.0.2.172"` {
			t.Fatalf("the alert reads bk_target_ip %s, want the instance's", got)
		}
		if got := string(event.RecordRef.Dimensions["bk_target_cloud_id"]); got != `0` {
			t.Fatalf("the alert reads bk_target_cloud_id %s, want the number 0", got)
		}
	}
	if len(closes) != 1 || !strings.Contains(closes[0], `"`+old+`"`) || strings.Contains(closes[0], current) ||
		!strings.Contains(closes[0], "superseded_identity") {
		t.Fatalf("closes by filter %v, want the alert under the record's own fingerprint %s closed once as superseded_identity",
			closes, old)
	}
}

// Aggregated by the service instance alone, the alert's target is the
// instance: the address Python's fuller writes is not part of its
// fingerprint, which stays the record's, while the record the alert carries
// names the instance's address and cloud, as Python's does.
func TestAServiceTargetedAlertKeepsItsFingerprintAndCarriesTheInstancesAddress(t *testing.T) {
	groupBy := []string{"bk_target_service_instance_id"}
	own := map[string]json.RawMessage{"bk_target_service_instance_id": json.RawMessage(`"7"`)}
	fingerprint := instanceFingerprint(t, groupBy, own)
	fixture := startInstanceFixture(t, groupBy)
	fixture.mustAttempt(1)
	fixture.sink.mu.Lock()
	written := append([]contract.TriggerEventV1(nil), fixture.sink.written...)
	fixture.sink.mu.Unlock()
	if len(written) == 0 {
		t.Fatal("no event was written")
	}
	for _, event := range written {
		if event.DedupeMD5 != fingerprint {
			t.Fatalf("%s filed under %s, want the record's own %s: the target is the instance", event.EventKind, event.DedupeMD5, fingerprint)
		}
		if ip, cloud := string(event.RecordRef.Dimensions["bk_target_ip"]), string(event.RecordRef.Dimensions["bk_target_cloud_id"]); ip != `"192.0.2.172"` || cloud != `0` {
			t.Fatalf("the alert's record reads bk_target_ip %s bk_target_cloud_id %s, want the instance's \"192.0.2.172\" and 0", ip, cloud)
		}
	}
}
