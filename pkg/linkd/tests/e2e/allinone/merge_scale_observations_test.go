// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package allinone_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
	"linkd/internal/config"
)

// configureScaleObservation 只由容量测试的显式诊断开关调用，生产配置保持默认关闭。
func configureScaleObservation(t *testing.T, document map[string]any) {
	t.Helper()
	listeners := make([]net.Listener, 0, 2)
	defer func() {
		for _, listener := range listeners {
			_ = listener.Close()
		}
	}()
	for range 2 {
		listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		listeners = append(listeners, listener)
	}
	document["logging"] = map[string]any{"level": "debug", "format": "json"}
	document["telemetry"] = map[string]any{
		"metrics":   map[string]any{"exporter": "prometheus", "prometheus": map[string]any{"listen_address": listeners[0].Addr().String()}},
		"profiling": map[string]any{"enabled": true, "listen_address": listeners[1].Addr().String(), "block_profile_rate": 1000000, "mutex_profile_fraction": 10},
	}
}

// scaleMetrics 返回累计计数和直方图 sum/count；调用方可对同一用例的阶段快照取差值。
// evidenceDir 必须是调用方为本轮 backend/size/budget 分配的独立目录。
func (h *policyHarness) scaleMetrics(label string, evidenceDir ...string) map[string]float64 {
	h.t.Helper()
	cfg, err := config.Load(h.configPath, config.Overrides{})
	if err != nil {
		h.t.Fatal(err)
	}
	if cfg.Telemetry == nil || cfg.Telemetry.Metrics.Exporter != config.TelemetryExporterPrometheus {
		return nil
	}
	ctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+cfg.Telemetry.Metrics.Prometheus.ListenAddress+"/metrics", nil)
	if err != nil {
		h.t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		h.t.Fatalf("merge metrics status %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (32<<20)+1))
	if err != nil || len(body) > 32<<20 {
		h.t.Fatalf("merge metrics response: bytes=%d err=%v", len(body), err)
	}
	if h.db != nil {
		body = append(body, []byte("\n# TYPE linkd_test_mysql_statements_total counter\n")...)
		// 只读本用例数据库的 SQL digest。采用参数插值的版本可观测物理语句数；
		// 老版本预编译执行未进入该表时不将缺失计数解释为零次写入。
		rows, err := h.db.QueryContext(ctx, `SELECT SUBSTRING_INDEX(DIGEST_TEXT,' ',1),SUM(COUNT_STAR) FROM performance_schema.events_statements_summary_by_digest WHERE SCHEMA_NAME=? GROUP BY SUBSTRING_INDEX(DIGEST_TEXT,' ',1)`, h.names.MySQLDatabase)
		if err != nil {
			h.t.Fatal(err)
		}
		for rows.Next() {
			var kind string
			var count int64
			if err := rows.Scan(&kind, &count); err != nil {
				_ = rows.Close()
				h.t.Fatal(err)
			}
			body = append(body, []byte(fmt.Sprintf("\nlinkd_test_mysql_statements_total{statement=%q} %d\n", kind, count))...)
		}
		readErr := rows.Err()
		_ = rows.Close()
		if readErr != nil {
			h.t.Fatal(readErr)
		}
	}
	snapshot, err := parseScaleMetrics(string(body))
	if err != nil {
		h.t.Fatal(err)
	}
	keys := make([]string, 0, len(snapshot))
	for key := range snapshot {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		h.t.Logf("MERGE_METRIC stage=%s %s=%.9g", label, key, snapshot[key])
	}
	if len(evidenceDir) > 0 && evidenceDir[0] != "" {
		if err := os.MkdirAll(evidenceDir[0], 0700); err != nil { //nolint:gosec // G703: 显式E2E运行指定的自有隔离诊断目录，不接受HTTP路径输入。
			h.t.Fatal(err)
		}
		filename := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
				return r
			}
			return '_'
		}, label)
		if err := os.WriteFile(filepath.Join(evidenceDir[0], filename+".prom"), body, 0600); err != nil { //nolint:gosec // G703: 只向本次E2E的隔离诊断目录写入指标文件，非HTTP输入路径。
			h.t.Fatal(err)
		}
	}
	return snapshot
}

// startScaleCPU 在真实阶段入口发起现有 pprof 请求，返回等待函数供测试结束前调用。
// 日志记录请求的实际起止时间；样本可能跨阶段，必须与 SCALE_PHASE 时间一并判读。
func (h *policyHarness) startScaleCPU(label, evidenceDir string, seconds int) func() {
	h.t.Helper()
	finished := make(chan error, 1)
	go func() {
		started := time.Now()
		h.t.Logf("SCALE_CPU label=%s request_start=%s seconds=%d", label, started.Format(time.RFC3339Nano), seconds)
		err := func() error {
			cfg, err := config.Load(h.configPath, config.Overrides{})
			if err != nil {
				return err
			}
			if cfg.Telemetry == nil || !cfg.Telemetry.Profiling.Enabled {
				return fmt.Errorf("merge CPU profiling is not enabled")
			}
			ctx, cancel := context.WithTimeout(h.ctx, time.Duration(seconds)*time.Second+10*time.Second)
			defer cancel()
			endpoint := "http://" + cfg.Telemetry.Profiling.ListenAddress + "/debug/pprof/profile?seconds=" + strconv.Itoa(seconds)
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
			if err != nil {
				return err
			}
			response, err := http.DefaultClient.Do(request)
			if err != nil {
				return err
			}
			defer func() { _ = response.Body.Close() }()
			if response.StatusCode != http.StatusOK {
				return fmt.Errorf("merge CPU profiling status %d", response.StatusCode)
			}
			if evidenceDir == "" {
				evidenceDir = filepath.Dir(h.configPath)
			}
			if err := os.MkdirAll(evidenceDir, 0700); err != nil { //nolint:gosec // G703: 目录来自显式E2E诊断配置或本用例临时目录，非HTTP输入。
				return err
			}
			path := filepath.Join(evidenceDir, label+".cpu.pprof")
			output, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600) //nolint:gosec // G304: 本次E2E在自有隔离诊断目录创建的profile文件，非HTTP输入路径。
			if err != nil {
				return err
			}
			_, copyErr := io.Copy(output, response.Body)
			return errors.Join(copyErr, output.Close())
		}()
		ended := time.Now()
		h.t.Logf("SCALE_CPU label=%s request_end=%s elapsed=%s error=%v", label, ended.Format(time.RFC3339Nano), ended.Sub(started), err)
		finished <- err
	}()
	var once sync.Once
	return func() {
		h.t.Helper()
		once.Do(func() {
			if err := <-finished; err != nil {
				h.t.Errorf("merge CPU profile %s: %v", label, err)
			}
		})
	}
}

func parseScaleMetrics(body string) (map[string]float64, error) {
	parser := expfmt.NewTextParser(model.LegacyValidation)
	families, err := parser.TextToMetricFamilies(strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	result := map[string]float64{}
	for name, family := range families {
		if !strings.HasPrefix(name, "linkd_merge_") && !strings.HasPrefix(name, "linkd_store_") && !strings.HasPrefix(name, "linkd_final_hook_") && !strings.HasPrefix(name, "linkd_elasticsearch_write_batch_") && !strings.HasPrefix(name, "linkd_lifecycle_lease_") && !strings.HasPrefix(name, "linkd_control_plane_task_") && !strings.HasPrefix(name, "linkd_lifecycle_event_phase_") && !strings.HasPrefix(name, "linkd_test_mysql_") {
			continue
		}
		for _, sample := range family.Metric {
			labels := make([]string, 0, len(sample.Label))
			for _, pair := range sample.Label {
				labels = append(labels, pair.GetName()+"="+strconv.Quote(pair.GetValue()))
			}
			slices.Sort(labels)
			suffix := "{" + strings.Join(labels, ",") + "}"
			switch {
			case sample.Gauge != nil && strings.HasPrefix(name, "linkd_merge_pool_"):
				result[name+suffix] = sample.Gauge.GetValue()
			case sample.Counter != nil:
				result[name+suffix] = sample.Counter.GetValue()
			case sample.Histogram != nil:
				result[name+"_sum"+suffix] = sample.Histogram.GetSampleSum()
				result[name+"_count"+suffix] = float64(sample.Histogram.GetSampleCount())
			}
		}
	}
	return result, nil
}

func TestParseScaleMetrics(t *testing.T) {
	body := `# TYPE linkd_store_operation_duration_seconds histogram
linkd_store_operation_duration_seconds_bucket{linkd_operation="get_current",linkd_object_type="alert",le="+Inf"} 2
linkd_store_operation_duration_seconds_sum{linkd_operation="get_current",linkd_object_type="alert"} 0.25
linkd_store_operation_duration_seconds_count{linkd_operation="get_current",linkd_object_type="alert"} 2
# TYPE linkd_lifecycle_lease_operations_total counter
linkd_lifecycle_lease_operations_total{linkd_outcome="succeeded",linkd_operation="acquire"} 3
# TYPE go_goroutines gauge
go_goroutines 8
# TYPE linkd_merge_pool_wait_duration_seconds gauge
linkd_merge_pool_wait_duration_seconds{linkd_pool="mysql"} 0.75
# TYPE linkd_control_plane_task_active_ratio gauge
linkd_control_plane_task_active_ratio{linkd_task="merge"} 1
`
	got, err := parseScaleMetrics(body)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]float64{
		`linkd_merge_pool_wait_duration_seconds{linkd_pool="mysql"}`:                                            0.75,
		`linkd_store_operation_duration_seconds_sum{linkd_object_type="alert",linkd_operation="get_current"}`:   0.25,
		`linkd_store_operation_duration_seconds_count{linkd_object_type="alert",linkd_operation="get_current"}`: 2,
		`linkd_lifecycle_lease_operations_total{linkd_operation="acquire",linkd_outcome="succeeded"}`:           3,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("snapshot=%v want=%v", got, want)
	}
	if _, err := parseScaleMetrics("bad{broken"); err == nil {
		t.Fatal("malformed metrics accepted")
	}
}

func TestConfigureScaleObservationUsesSeparateLoopbackPorts(t *testing.T) {
	document := map[string]any{"control_plane": map[string]any{"merge": map[string]any{"member_batch_size": 128}}}
	configureScaleObservation(t, document)
	telemetry := document["telemetry"].(map[string]any)
	metrics := telemetry["metrics"].(map[string]any)
	metricAddress := metrics["prometheus"].(map[string]any)["listen_address"].(string)
	profile := telemetry["profiling"].(map[string]any)
	profileAddress := profile["listen_address"].(string)
	for _, address := range []string{metricAddress, profileAddress} {
		host, port, err := net.SplitHostPort(address)
		if err != nil || host != "127.0.0.1" || port == "0" {
			t.Fatalf("invalid diagnostic endpoint %q: %v", address, err)
		}
	}
	if metricAddress == profileAddress || profile["enabled"] != true || metrics["exporter"] != "prometheus" {
		t.Fatalf("unexpected telemetry configuration: %#v", telemetry)
	}
	if document["control_plane"].(map[string]any)["merge"].(map[string]any)["member_batch_size"] != 128 {
		t.Fatal("observation changed merge budget")
	}
}

func TestStartScaleCPURequestsAsyncAndPersistsCaseEvidence(t *testing.T) {
	received := make(chan string, 1)
	proceed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received <- r.URL.String()
		select {
		case <-proceed:
			_, _ = w.Write([]byte("synthetic-profile"))
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(proceed)
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	body := "telemetry:\n  profiling:\n    enabled: true\n    listen_address: " + strings.TrimPrefix(server.URL, "http://") + "\n"
	if err := os.WriteFile(configPath, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	h := &policyHarness{t: t, ctx: t.Context(), configPath: configPath}
	directory := filepath.Join(t.TempDir(), "case")
	wait := h.startScaleCPU("linking", directory, 17)
	select {
	case path := <-received:
		if path != "/debug/pprof/profile?seconds=17" {
			t.Fatalf("unexpected profile request %q", path)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("async profile request did not start")
	}
	// 服务尚未完成响应时启动函数已经返回；等待只发生在调用方明确收口时。
	proceed <- struct{}{}
	wait()
	wait()
	path := filepath.Join(directory, "linking.cpu.pprof")
	data, err := os.ReadFile(path) //nolint:gosec // G304: 只读取本用例在t.TempDir中创建的固定profile fixture。
	if err != nil || string(data) != "synthetic-profile" {
		t.Fatalf("profile evidence=%q err=%v", data, err)
	}
	for _, tc := range []struct {
		path string
		mode os.FileMode
	}{{directory, 0700}, {path, 0600}} {
		info, err := os.Stat(tc.path)
		if err != nil || info.Mode().Perm() != tc.mode {
			t.Fatalf("evidence permissions %q: %v %v", tc.path, info, err)
		}
	}
}
