// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmd

import (
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

const (
	syncStorageKey     = "optional:uq:data:storage:1"
	syncStorageChannel = "optional:uq:data:storage:storage_channel"
	syncFlagsChannel   = optionalConsulFlagsKey + ":feature_flag_channel"
	syncTableChannel   = "optional:spaces:result_table_detail:channel"
	syncTables         = "optional:spaces:result_table_detail"
	syncSpaces         = "optional:spaces:space_to_result_table"
	syncStorage        = `{"type":"victoria_metrics","address":"http://vm.invalid"}`
	syncTable          = `{"table_id":"%s","storage_id":1,"storage_type":"victoria_metrics","storage_name":"vm-test","db":"sync","measurement":"metric","fields":["value"],"measurement_type":"bk_traditional_measurement","vm_rt":"2_sync_metric"}`
)

// metrics reads the running process's /metrics page.
func (p *rootProcess) metrics() string {
	p.t.Helper()
	response, err := (&http.Client{Timeout: 5 * time.Second}).Get(p.baseURL + "/metrics")
	if err != nil {
		p.t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != http.StatusOK {
		p.t.Fatalf("/metrics returned %d: %v", response.StatusCode, err)
	}
	return string(data)
}

// get requests a diagnostic page of the running process and discards what it prints.
func (p *rootProcess) get(path string) {
	p.t.Helper()
	response, err := (&http.Client{Timeout: 10 * time.Second}).Get(p.baseURL + path)
	if err != nil {
		p.t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
}

// seriesValue finds the sample of one series, written as the exposition format writes it.
func seriesValue(page, series string) (float64, bool) {
	for _, line := range strings.Split(page, "\n") {
		if value, found := strings.CutPrefix(line, series+" "); found {
			number, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			return number, err == nil
		}
	}
	return 0, false
}

func configSyncLines(page string) string {
	var lines []string
	for _, line := range strings.Split(page, "\n") {
		if strings.HasPrefix(line, "unify_query_config_sync_") || strings.HasPrefix(line, "unify_query_redis_router_load_total") {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}

// waitSeries waits until the series has the value, and fails with the config sync lines otherwise.
func (p *rootProcess) waitSeries(series string, want float64, within time.Duration) {
	p.t.Helper()
	deadline := time.Now().Add(within)
	for {
		page := p.metrics()
		if got, ok := seriesValue(page, series); ok && got == want {
			return
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("%s did not become %v within %s:\n%s", series, want, within, configSyncLines(page))
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (p *rootProcess) series(series string) (float64, bool) {
	p.t.Helper()
	return seriesValue(p.metrics(), series)
}

func failures(source, reason string) string {
	return `unify_query_config_sync_failures_total{reason="` + reason + `",source="` + source + `"}`
}

func up(source string) string {
	return `unify_query_config_sync_up{source="` + source + `"}`
}

func lastSuccess(source string) string {
	return `unify_query_config_sync_last_success_timestamp_seconds{source="` + source + `"}`
}

func seedConfigSync(t *testing.T, redis *miniredis.Miniredis) {
	t.Helper()
	if err := redis.Set(syncStorageKey, syncStorage); err != nil {
		t.Fatal(err)
	}
	if err := redis.Set(optionalConsulFlagsKey, optionalConsulRules); err != nil {
		t.Fatal(err)
	}
	redis.HSet(syncSpaces, "bkcc__2", `{"sync.metric":{"table_id":"sync.metric","filters":[]}}`)
	redis.HSet(syncTables, "sync.metric", strings.ReplaceAll(syncTable, "%s", "sync.metric"))
}

// The sources a Redis-only deployment syncs, driven through Redis and the process's own diagnostic
// pages: every series exists from startup, each failure is counted under its reason, and the
// subscription and watches go down with Redis and come back with it.
func TestConfigSyncMetricsFollowRedis(t *testing.T) {
	redis := miniredis.RunT(t)
	seedConfigSync(t, redis)
	p := startRootProcess(t, redis, nil, "redis", "UNIFY-QUERY_INFLUXDB_ROUTER_INTERNAL=45m")

	// The effective full reload period, and a startup reload in which every key loaded.
	p.waitSeries(`unify_query_config_sync_interval_seconds{source="space_router_full"}`, 2700, 10*time.Second)
	for _, source := range []string{"space_router_incremental", "storage", "feature_flag"} {
		p.waitSeries(up(source), 1, 10*time.Second)
		for _, reason := range []string{"backend", "decode", "canceled", "other"} {
			if got, ok := p.series(failures(source, reason)); !ok || got != 0 {
				t.Fatalf("%s/%s: want a 0 counter from startup, got %v %v", source, reason, got, ok)
			}
		}
	}
	for _, source := range []string{"space_router_full", "space_router_incremental", "storage", "feature_flag"} {
		if at, ok := p.series(lastSuccess(source)); !ok || at < 1.7e9 {
			t.Fatalf("%s: want a startup sync time, got %v %v", source, at, ok)
		}
	}
	page := p.metrics()
	if strings.Contains(page, `unify_query_config_sync_failures_total{reason="backend",source="space_router_full"}`) ||
		strings.Contains(page, `unify_query_config_sync_up{source="space_router_full"}`) ||
		strings.Contains(page, `unify_query_config_sync_interval_seconds{source="storage"}`) {
		t.Fatalf("space_router_full has no failure or up series and only it has an interval:\n%s", configSyncLines(page))
	}

	// An update applied moves the incremental timestamp; one naming a key Redis does not hold is other;
	// one whose value does not parse is decode.
	before, _ := p.series(lastSuccess("space_router_incremental"))
	time.Sleep(20 * time.Millisecond)
	redis.HSet(syncTables, "late.metric", strings.ReplaceAll(syncTable, "%s", "late.metric"))
	if receivers := redis.Publish(syncTableChannel, "late.metric"); receivers != 1 {
		t.Fatalf("want the space router subscribed, got %d receivers", receivers)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		if after, _ := p.series(lastSuccess("space_router_incremental")); after > before {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("an applied update did not move the incremental timestamp:\n%s", configSyncLines(p.metrics()))
		}
		time.Sleep(50 * time.Millisecond)
	}
	redis.Publish(syncTableChannel, "missing.metric")
	p.waitSeries(failures("space_router_incremental", "other"), 1, 5*time.Second)
	redis.HSet(syncTables, "bad.metric", `{"table_id":`)
	redis.Publish(syncTableChannel, "bad.metric")
	p.waitSeries(failures("space_router_incremental", "decode"), 1, 5*time.Second)
	if got, _ := p.series(failures("space_router_incremental", "backend")); got != 0 {
		t.Fatalf("Redis answered every update read, got backend %v", got)
	}

	// Storage that does not parse fails both watchers that read it, and Redis still answered.
	if err := redis.Set(syncStorageKey, `{"type":`); err != nil {
		t.Fatal(err)
	}
	redis.Publish(syncStorageChannel, "changed")
	p.waitSeries(failures("storage", "decode"), 2, 5*time.Second)
	if got, _ := p.series(up("storage")); got != 1 {
		t.Fatalf("a decode failure is not a lost watch, got up %v", got)
	}
	if err := redis.Set(syncStorageKey, syncStorage); err != nil {
		t.Fatal(err)
	}
	// Feature flags that do not validate are decode as well.
	if err := redis.Set(optionalConsulFlagsKey, `{"startup-flag":`); err != nil {
		t.Fatal(err)
	}
	redis.Publish(syncFlagsChannel, "changed")
	p.waitSeries(failures("feature_flag", "decode"), 1, 5*time.Second)
	if err := redis.Set(optionalConsulFlagsKey, optionalConsulRules); err != nil {
		t.Fatal(err)
	}

	// Redis goes away: the subscription notices by itself; the watches notice on their next read.
	redis.Close()
	p.waitSeries(up("space_router_incremental"), 0, 10*time.Second)
	p.get("/tsdb_print?r=1")
	p.get("/ff?r=1")
	p.waitSeries(failures("storage", "backend"), 1, 5*time.Second)
	p.waitSeries(failures("feature_flag", "backend"), 1, 5*time.Second)
	p.waitSeries(up("storage"), 0, time.Second)
	p.waitSeries(up("feature_flag"), 0, time.Second)

	// Redis comes back: the subscription is confirmed again; the watches are up on their next read.
	if err := redis.Restart(); err != nil {
		t.Fatal(err)
	}
	p.waitSeries(up("space_router_incremental"), 1, 15*time.Second)
	p.get("/tsdb_print?r=1")
	p.get("/ff?r=1")
	p.waitSeries(up("storage"), 1, 5*time.Second)
	p.waitSeries(up("feature_flag"), 1, 5*time.Second)
	if got, _ := p.series(failures("storage", "backend")); got != 1 {
		t.Fatalf("the read after Redis returned must succeed, got backend %v", got)
	}
	p.stop()
}

// A startup reload that failed leaves no timestamp, and a source the deployment does not use has no
// series at all.
func TestConfigSyncMetricsAbsentWithoutASuccessOrASource(t *testing.T) {
	redis := miniredis.RunT(t)
	if err := redis.Set(optionalConsulFlagsKey, optionalConsulRules); err != nil {
		t.Fatal(err)
	}
	redis.HSet(syncSpaces, "bkcc__2", `{"sync.metric":`)
	// Storage is read from Consul and no Consul is configured.
	p := startRootProcess(t, redis, nil, "consul")

	p.waitSeries(`unify_query_config_sync_interval_seconds{source="space_router_full"}`, 1800, 10*time.Second)
	p.waitSeries(up("space_router_incremental"), 1, 10*time.Second)
	page := p.metrics()
	if _, ok := seriesValue(page, lastSuccess("space_router_full")); ok {
		t.Fatalf("a failed startup reload must leave no timestamp:\n%s", configSyncLines(page))
	}
	failed := false
	for _, line := range strings.Split(page, "\n") {
		if strings.HasPrefix(line, "unify_query_redis_router_load_total{") && strings.Contains(line, `result="failure"`) &&
			strings.Contains(line, `route_key="space_to_result_table"`) && strings.HasSuffix(line, " 1") {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("the failed key is counted where it always was:\n%s", configSyncLines(page))
	}
	if strings.Contains(page, `source="storage"`) {
		t.Fatalf("storage is not used by this deployment and must have no series:\n%s", configSyncLines(page))
	}
	if _, ok := seriesValue(page, up("feature_flag")); !ok {
		t.Fatalf("feature flags are still read from Redis:\n%s", configSyncLines(page))
	}
	p.stop()
}
