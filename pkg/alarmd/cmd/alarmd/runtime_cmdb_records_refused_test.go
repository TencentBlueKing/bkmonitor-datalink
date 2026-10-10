// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/cmdbcache"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/fleet"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
)

// refusedRecordCells reads the refused-records gauge by record kind.
func refusedRecordCells(t *testing.T, recorder *metric.Recorder) map[string]float64 {
	t.Helper()
	families, err := recorder.Gatherer().Gather()
	if err != nil {
		t.Fatal(err)
	}
	cells := map[string]float64{}
	for _, family := range families {
		if family.GetName() != "bkmonitor_alarmd_cmdb_index_records_refused" {
			continue
		}
		for _, sample := range family.GetMetric() {
			for _, label := range sample.GetLabel() {
				cells[label.GetValue()] = sample.GetGauge().GetValue()
			}
		}
	}
	return cells
}

// refusalLines is every records_refused line the log holds, decoded.
func refusalLines(t *testing.T, output *bytes.Buffer) []map[string]any {
	t.Helper()
	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(output.String()), "\n") {
		if line == "" {
			continue
		}
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("log line is not JSON: %q", line)
		}
		if event["stage"] == "cmdb_index" && event["result"] == "records_refused" {
			lines = append(lines, event)
		}
	}
	return lines
}

// What a CMDB index load refused is published as that load's counts, from
// the production wiring: on the gauge and the cmdb_cache writer evidence
// after every refresh, and in the log once when the counts change - the
// worker's first load, and the load that reads clean again - not on a
// refresh that refuses as many again.
func TestWhatACMDBIndexLoadRefusedIsPublishedAsThatLoadsCounts(t *testing.T) {
	_, client := startPhaseTwoRedis(t)
	ctx := context.Background()
	cfg := config.Default()
	cfg.Kafka.LegacyAdapter.SnapshotPrefix = "bk_monitorv3.test.cache"
	hosts := cfg.PlatformKeyPrefix() + ".cache.cmdb.host"
	instances := cfg.PlatformKeyPrefix() + ".cache.cmdb.service_instance"
	// One host under both of its fields, with one topology node the reader
	// cannot index; one host whose record does not decode, under both of
	// its fields; one service instance whose record does not decode.
	good := `{"bk_host_id":1,"bk_host_innerip":"192.0.2.1","bk_cloud_id":0,"bk_biz_id":2,"topo_link":{"module|1":[` +
		`{"bk_obj_id":"module","bk_inst_id":1},{"bk_obj_id":"set","bk_inst_id":"rack-a"},{"bk_obj_id":"biz","bk_inst_id":2}]}}`
	if err := client.HSet(ctx, hosts, "192.0.2.1|0", good, "1", good, "192.0.2.2|0", "{", "2", "{").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HSet(ctx, instances, "11", "not a record").Err(); err != nil {
		t.Fatal(err)
	}

	var output bytes.Buffer
	logger := observability.New(observability.ComponentRuntime, &output)
	recorder := metric.NewRecorder(metric.BuildInfo{})
	_, store, err := buildSeriesAdmission(ctx, cfg, client, recorder, logger, nil, startupWaiter{initial: time.Millisecond, ceiling: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	refresh := func() {
		t.Helper()
		// What maintainCMDBIndex does on every tick.
		if err := store.Refresh(ctx); err != nil {
			t.Fatal(err)
		}
		publishCMDBIndexHealth(recorder, store)
	}
	published := func(host, instance, node int) *fleet.RefusedRecords {
		t.Helper()
		cells := refusedRecordCells(t, recorder)
		if len(cells) != 3 || cells["host"] != float64(host) || cells["service_instance"] != float64(instance) || cells["topo_node"] != float64(node) {
			t.Fatalf("gauge = %v, want host %d, service_instance %d, topo_node %d", cells, host, instance, node)
		}
		endpoints := endpointFactsSource(cfg, endpointSharing{}, recorder, store, nil,
			func() *fleet.SourceFacts { return nil }, nil, nil, time.Now)()
		for _, entry := range endpoints {
			if entry.Role != fleet.EndpointCMDBCache {
				continue
			}
			if entry.Writer == nil || entry.Writer.Refused == nil {
				t.Fatalf("cmdb_cache writer = %+v, want what the load refused", entry.Writer)
			}
			refused := entry.Writer.Refused
			if refused.Host != host || refused.ServiceInstance != instance || refused.TopoNode != node {
				t.Fatalf("cmdb_cache writer refused = %+v, want host %d, service_instance %d, topo_node %d", refused, host, instance, node)
			}
			return refused
		}
		t.Fatal("no cmdb_cache endpoint")
		return nil
	}

	// The worker's first load: both fields of the bad host, the instance,
	// the one node, each first named by the field it was read under.
	refused := published(2, 1, 1)
	if (refused.FirstHost != "192.0.2.2|0" && refused.FirstHost != "2") || refused.FirstServiceInstance != "11" ||
		(refused.FirstTopoNode != "host:192.0.2.1|0" && refused.FirstTopoNode != "host:1") {
		t.Fatalf("first refused = %+v, want the bad host's field, instance 11, the good host's field for the node", refused)
	}
	lines := refusalLines(t, &output)
	if len(lines) != 1 || lines[0]["level"] != "WARN" || lines[0]["records"] != float64(4) || lines[0]["host"] != float64(2) ||
		lines[0]["service_instance"] != float64(1) || lines[0]["topo_node"] != float64(1) ||
		lines[0]["first_host"] != refused.FirstHost || lines[0]["first_service_instance"] != "11" || lines[0]["first_topo_node"] != refused.FirstTopoNode {
		t.Fatalf("log after the first load = %v, want one warning with the three counts and the first of each", lines)
	}

	// The writer publishes the same records again: the same counts, no line.
	refresh()
	published(2, 1, 1)
	if lines := refusalLines(t, &output); len(lines) != 1 {
		t.Fatalf("a refresh refusing as many again logged: %v", lines)
	}

	// The writer puts its records right: every count back to none, said once.
	fixed := strings.Replace(good, `"rack-a"`, `4`, 1)
	if err := client.HSet(ctx, hosts, "192.0.2.1|0", fixed, "1", fixed).Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.HDel(ctx, hosts, "192.0.2.2|0", "2").Err(); err != nil {
		t.Fatal(err)
	}
	if err := client.Del(ctx, instances).Err(); err != nil {
		t.Fatal(err)
	}
	refresh()
	if refused := published(0, 0, 0); *refused != (fleet.RefusedRecords{}) {
		t.Fatalf("a clean load names %+v", refused)
	}
	lines = refusalLines(t, &output)
	if len(lines) != 2 || lines[1]["level"] != "INFO" || lines[1]["records"] != float64(0) || lines[1]["host"] != float64(0) ||
		lines[1]["service_instance"] != float64(0) || lines[1]["topo_node"] != float64(0) {
		t.Fatalf("log after the clean load = %v, want a second line with every count at zero", lines)
	}
	refresh()
	if lines := refusalLines(t, &output); len(lines) != 2 {
		t.Fatalf("a second clean refresh logged: %v", lines)
	}
}

// Before the first load the cmdb_cache writer evidence says nothing about
// refused records: none were read, which is not the same as none refused.
func TestTheCMDBWriterEvidenceNamesNoRefusalsBeforeAFirstLoad(t *testing.T) {
	cfg := config.Default()
	cfg.Kafka.LegacyAdapter.SnapshotPrefix = "bk_monitorv3.test.cache"
	store, err := cmdbcache.NewStore(emptyIndexLoader{}, cmdbcache.StoreOptions{RefreshInterval: time.Minute, MaxAge: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	endpoints := endpointFactsSource(cfg, endpointSharing{}, metric.NewRecorder(metric.BuildInfo{}), store, nil,
		func() *fleet.SourceFacts { return nil }, nil, nil, time.Now)()
	for _, entry := range endpoints {
		if entry.Role == fleet.EndpointCMDBCache {
			if entry.Writer == nil || entry.Writer.Present || entry.Writer.Refused != nil {
				t.Fatalf("cmdb_cache writer before a load = %+v, want not present and no refused records", entry.Writer)
			}
			return
		}
	}
	t.Fatal("no cmdb_cache endpoint")
}
