// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metric

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestConfigSyncReason(t *testing.T) {
	var syntax error = &json.SyntaxError{}
	var typed error = &json.UnmarshalTypeError{}
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"plain read failure":       {errors.New("dial tcp: connection refused"), "backend"},
		"wrapped read failure":     {fmt.Errorf("scan: %w", errors.New("i/o timeout")), "backend"},
		"marked decode":            {fmt.Errorf("load: %w", ConfigSyncDecodeError(errors.New("bad value"))), "decode"},
		"encoding/json syntax":     {fmt.Errorf("get: %w", syntax), "decode"},
		"encoding/json type":       {typed, "decode"},
		"marked apply":             {ConfigSyncApplyError(errors.New("bbolt: write failed")), "other"},
		"canceled":                 {fmt.Errorf("read: %w", context.Canceled), "canceled"},
		"canceled inside a marker": {ConfigSyncApplyError(fmt.Errorf("apply: %w", context.Canceled)), "canceled"},
		"deadline is the backend":  {context.DeadlineExceeded, "backend"},
	} {
		if got := ConfigSyncReason(tc.err); got != tc.want {
			t.Errorf("%s: got %s, want %s", name, got, tc.want)
		}
	}
	if ConfigSyncDecodeError(nil) != nil || ConfigSyncApplyError(nil) != nil {
		t.Fatal("marking no error must stay no error")
	}
	marked := ConfigSyncApplyError(errors.New("same message"))
	if marked.Error() != "same message" {
		t.Fatalf("marking must not change the message, got %q", marked.Error())
	}
	if !ConfigSyncBackendAnswered(nil) || !ConfigSyncBackendAnswered(ConfigSyncDecodeError(errors.New("x"))) ||
		ConfigSyncBackendAnswered(errors.New("connection refused")) {
		t.Fatal("only a failed read of the backend means the backend did not answer")
	}
}

func seriesCount(t *testing.T, collector prometheus.Collector, labels map[string]string) int {
	t.Helper()
	registry := prometheus.NewPedanticRegistry()
	registry.MustRegister(collector)
	families, err := registry.Gather()
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, family := range families {
		for _, sample := range family.GetMetric() {
			matched := 0
			for _, pair := range sample.GetLabel() {
				if value, ok := labels[pair.GetName()]; ok && value == pair.GetValue() {
					matched++
				}
			}
			if matched == len(labels) {
				count++
			}
		}
	}
	return count
}

func TestConfigSyncSeries(t *testing.T) {
	ConfigSyncEnable(ConfigSyncSourceFeatureFlag)
	if got := seriesCount(t, configSyncFailures, map[string]string{"source": "feature_flag"}); got != 4 {
		t.Fatalf("want a counter per reason from enabling, got %d", got)
	}
	if got := testutil.ToFloat64(configSyncUp.WithLabelValues("feature_flag")); got != 0 {
		t.Fatalf("up starts at 0, got %v", got)
	}
	if got := seriesCount(t, configSyncLastSuccess, map[string]string{"source": "feature_flag"}); got != 0 {
		t.Fatalf("no timestamp before the first success, got %d series", got)
	}
	before := float64(time.Now().Unix())
	ConfigSyncSucceeded(ConfigSyncSourceFeatureFlag)
	if got := testutil.ToFloat64(configSyncLastSuccess.WithLabelValues("feature_flag")); got < before {
		t.Fatalf("want the time of the success, got %v", got)
	}

	ConfigSyncEnable(ConfigSyncSourceSpaceRouterFull)
	ConfigSyncFailed(context.Background(), ConfigSyncSourceSpaceRouterFull, errors.New("x"))
	ConfigSyncSetUp(ConfigSyncSourceSpaceRouterFull, "full", true)
	ConfigSyncSetInterval(ConfigSyncSourceStorage, time.Minute)
	ConfigSyncSucceeded("unknown")
	if seriesCount(t, configSyncFailures, map[string]string{"source": "space_router_full"}) != 0 ||
		seriesCount(t, configSyncUp, map[string]string{"source": "space_router_full"}) != 0 ||
		seriesCount(t, configSyncInterval, map[string]string{"source": "storage"}) != 0 ||
		seriesCount(t, configSyncLastSuccess, map[string]string{"source": "unknown"}) != 0 {
		t.Fatal("space_router_full has no failure or up series, only it has an interval, and sources are closed")
	}
	ConfigSyncSetInterval(ConfigSyncSourceSpaceRouterFull, 45*time.Minute)
	if got := testutil.ToFloat64(configSyncInterval.WithLabelValues("space_router_full")); got != 2700 {
		t.Fatalf("interval in seconds, got %v", got)
	}

	ConfigSyncFailed(context.Background(), ConfigSyncSourceFeatureFlag, ConfigSyncDecodeError(errors.New("x")))
	if got := testutil.ToFloat64(configSyncFailures.WithLabelValues("feature_flag", "decode")); got != 1 {
		t.Fatalf("one decode failure, got %v", got)
	}
}

func TestConfigSyncUpNeedsEveryWatcher(t *testing.T) {
	configSyncUpMu.Lock()
	delete(configSyncUpParts, ConfigSyncSourceStorage)
	configSyncUpMu.Unlock()
	value := func() float64 { return testutil.ToFloat64(configSyncUp.WithLabelValues("storage")) }

	// Both services enable storage; a watcher that has not reported yet counts as down.
	ConfigSyncEnable(ConfigSyncSourceStorage, ConfigSyncStorageWatchers...)
	if value() != 0 {
		t.Fatal("no watcher has reported")
	}
	ConfigSyncSetUp(ConfigSyncSourceStorage, ConfigSyncWatcherTsDBStorage, true)
	if value() != 0 {
		t.Fatal("one watcher live and the other still to report is not up")
	}
	// The second service enabling storage does not reset the watcher that already reported.
	ConfigSyncEnable(ConfigSyncSourceStorage, ConfigSyncStorageWatchers...)
	ConfigSyncSetUp(ConfigSyncSourceStorage, ConfigSyncWatcherInfluxDB, true)
	if value() != 1 {
		t.Fatal("both watchers live")
	}
	ConfigSyncSetUp(ConfigSyncSourceStorage, ConfigSyncWatcherInfluxDB, false)
	if value() != 0 {
		t.Fatal("one watcher down takes the source down")
	}
	ConfigSyncSetUp(ConfigSyncSourceStorage, ConfigSyncWatcherTsDBStorage, true)
	if value() != 0 {
		t.Fatal("the other watcher reporting again does not hide the one that is down")
	}
	ConfigSyncSetUp(ConfigSyncSourceStorage, ConfigSyncWatcherInfluxDB, true)
	if value() != 1 {
		t.Fatal("both watchers live again")
	}
}

// A sync of a source the deployment did not enable, such as a diagnostic refresh of storage read from
// Consul with no Consul configured, leaves the source without series.
func TestConfigSyncLeavesASourceNotEnabledWithoutSeries(t *testing.T) {
	configSyncUpMu.Lock()
	delete(configSyncUpParts, ConfigSyncSourceStorage)
	configSyncUpMu.Unlock()
	configSyncUp.DeleteLabelValues(ConfigSyncSourceStorage)
	configSyncLastSuccess.DeleteLabelValues(ConfigSyncSourceStorage)
	for _, reason := range configSyncReasons {
		configSyncFailures.DeleteLabelValues(ConfigSyncSourceStorage, reason)
	}
	collectors := map[string]prometheus.Collector{"failures": configSyncFailures, "up": configSyncUp, "last success": configSyncLastSuccess}

	ConfigSyncFailed(context.Background(), ConfigSyncSourceStorage, errors.New("dial tcp: connection refused"))
	ConfigSyncSetUp(ConfigSyncSourceStorage, ConfigSyncWatcherInfluxDB, false)
	ConfigSyncSucceeded(ConfigSyncSourceStorage)
	for name, collector := range collectors {
		if got := seriesCount(t, collector, map[string]string{"source": "storage"}); got != 0 {
			t.Fatalf("%s: storage was not enabled and must have no series, got %d", name, got)
		}
	}

	// Positive control: once the source is enabled, the same calls write it.
	ConfigSyncEnable(ConfigSyncSourceStorage, ConfigSyncStorageWatchers...)
	ConfigSyncFailed(context.Background(), ConfigSyncSourceStorage, errors.New("dial tcp: connection refused"))
	ConfigSyncSucceeded(ConfigSyncSourceStorage)
	if got := testutil.ToFloat64(configSyncFailures.WithLabelValues("storage", "backend")); got != 1 {
		t.Fatalf("one backend failure once enabled, got %v", got)
	}
	for name, collector := range map[string]prometheus.Collector{"up": configSyncUp, "last success": configSyncLastSuccess} {
		if got := seriesCount(t, collector, map[string]string{"source": "storage"}); got != 1 {
			t.Fatalf("%s: want the series once enabled, got %d", name, got)
		}
	}
}
