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
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Sources unify-query keeps its routing and storage configuration in step with.
const (
	// ConfigSyncSourceSpaceRouterFull is the timed HScan reload of every space router key.
	ConfigSyncSourceSpaceRouterFull = "space_router_full"
	// ConfigSyncSourceSpaceRouterIncremental is the space router's Redis Pub/Sub updates.
	ConfigSyncSourceSpaceRouterIncremental = "space_router_incremental"
	// ConfigSyncSourceStorage is the storage map, from Consul or Redis.
	ConfigSyncSourceStorage = "storage"
	// ConfigSyncSourceFeatureFlag is the feature flag snapshot, from Redis or Consul.
	ConfigSyncSourceFeatureFlag = "feature_flag"
)

// Why one sync of a source failed.
const (
	// ConfigSyncReasonBackend: the read from Redis or Consul failed.
	ConfigSyncReasonBackend = "backend"
	// ConfigSyncReasonDecode: what the backend returned did not parse or validate.
	ConfigSyncReasonDecode = "decode"
	// ConfigSyncReasonCanceled: the sync's context was canceled.
	ConfigSyncReasonCanceled = "canceled"
	// ConfigSyncReasonOther: applying what was read failed, or the update named nothing that exists.
	ConfigSyncReasonOther = "other"
)

var configSyncReasons = []string{
	ConfigSyncReasonBackend, ConfigSyncReasonDecode, ConfigSyncReasonCanceled, ConfigSyncReasonOther,
}

var (
	configSyncLastSuccess = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "unify_query", Name: "config_sync_last_success_timestamp_seconds",
		Help: "unix time of the last sync that left the source current: space_router_full, a reload in which every " +
			"space router key loaded; space_router_incremental, a subscription Redis confirmed or an update applied; " +
			"storage and feature_flag, a read of the backend applied, changed or not. Absent until the first one, " +
			"so a failed startup reload leaves no series",
	}, []string{"source"})
	configSyncFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Namespace: "unify_query", Name: "config_sync_failures_total",
		Help: "failed syncs by source and reason: backend, the read from Redis or Consul failed; decode, what came " +
			"back did not parse or validate; canceled; other, applying it failed or the update named nothing that " +
			"exists. space_router_full failures are unify_query_redis_router_load_total{result=\"failure\"}",
	}, []string{"source", "reason"})
	configSyncInterval = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "unify_query", Name: "config_sync_interval_seconds",
		Help: "effective period, in seconds, of a source reloaded on a timer, as the influxdb router interval " +
			"setting configures it; only space_router_full has one, set at startup and on every config reload",
	}, []string{"source"})
	configSyncUp = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Namespace: "unify_query", Name: "config_sync_up",
		Help: "1 while the source's subscription or watch is live, else 0. space_router_incremental: Redis " +
			"confirmed the subscription and no receive or health-check ping has failed since. storage and " +
			"feature_flag: the watch is open and the latest sync reached the backend (did not fail with reason " +
			"backend); their watches retry " +
			"inside the Consul and Redis clients without reporting a lost connection, so the once-a-minute " +
			"reconcile read is what notices one, and this can lag a backend outage by up to a minute. storage " +
			"is read by two watchers, the storage map queries use and the InfluxDB host list, and is up only " +
			"while both are",
	}, []string{"source"})

	configSyncUpMu    sync.Mutex
	configSyncUpParts = map[string]map[string]bool{}
)

// Watchers that keep one source in step. storage has two: the tsdb storage map every query reads and
// the InfluxDB host list, both read from the same storage configuration.
const (
	ConfigSyncWatcherSpaceRouter = "space_router"
	ConfigSyncWatcherFeatureFlag = "feature_flag"
	ConfigSyncWatcherTsDBStorage = "tsdb_storage"
	ConfigSyncWatcherInfluxDB    = "influxdb_storage"
)

func configSyncWatchSource(source string) bool {
	switch source {
	case ConfigSyncSourceSpaceRouterIncremental, ConfigSyncSourceStorage, ConfigSyncSourceFeatureFlag:
		return true
	}
	return false
}

// ConfigSyncEnable creates the series of a subscribed or watched source this deployment uses: up at 0
// until the subscription or watch is live, and a failure counter at 0 for every reason. A source the
// deployment does not use is never enabled and has no series. The timestamp is created by the first
// success, never preset to 0.
func ConfigSyncEnable(source string) {
	if !configSyncWatchSource(source) {
		return
	}
	configSyncUp.WithLabelValues(source)
	for _, reason := range configSyncReasons {
		configSyncFailures.WithLabelValues(source, reason)
	}
}

// ConfigSyncSucceeded records now as the source's last successful sync.
func ConfigSyncSucceeded(source string) {
	if source != ConfigSyncSourceSpaceRouterFull && !configSyncWatchSource(source) {
		return
	}
	configSyncLastSuccess.WithLabelValues(source).Set(float64(time.Now().UnixNano()) / float64(time.Second))
}

// ConfigSyncFailed counts one failed sync of a subscribed or watched source, with err's reason.
func ConfigSyncFailed(ctx context.Context, source string, err error) {
	if err == nil || !configSyncWatchSource(source) {
		return
	}
	counterInc(ctx, configSyncFailures.WithLabelValues(source, ConfigSyncReason(err)))
}

// ConfigSyncSetUp sets whether one watcher of the source is live. The source is up only while every
// watcher of it that has reported is.
func ConfigSyncSetUp(source, watcher string, up bool) {
	if !configSyncWatchSource(source) {
		return
	}
	configSyncUpMu.Lock()
	defer configSyncUpMu.Unlock()
	parts := configSyncUpParts[source]
	if parts == nil {
		parts = map[string]bool{}
		configSyncUpParts[source] = parts
	}
	parts[watcher] = up
	value := 1.0
	for _, live := range parts {
		if !live {
			value = 0
		}
	}
	configSyncUp.WithLabelValues(source).Set(value)
}

// ConfigSyncSetInterval sets the effective period of a source reloaded on a timer.
func ConfigSyncSetInterval(source string, interval time.Duration) {
	if source != ConfigSyncSourceSpaceRouterFull || interval <= 0 {
		return
	}
	configSyncInterval.WithLabelValues(source).Set(interval.Seconds())
}

type configSyncError struct {
	reason string
	err    error
}

func (e *configSyncError) Error() string { return e.err.Error() }

func (e *configSyncError) Unwrap() error { return e.err }

// ConfigSyncDecodeError marks err as configuration read from the backend that did not parse or
// validate. The message and the error chain are unchanged.
func ConfigSyncDecodeError(err error) error {
	if err == nil {
		return nil
	}
	return &configSyncError{reason: ConfigSyncReasonDecode, err: err}
}

// ConfigSyncApplyError marks err as a failure after the backend answered: applying what was read, or an
// update naming nothing that exists. The message and the error chain are unchanged.
func ConfigSyncApplyError(err error) error {
	if err == nil {
		return nil
	}
	return &configSyncError{reason: ConfigSyncReasonOther, err: err}
}

// ConfigSyncBackendAnswered says whether a sync that ended with err reached its backend: it succeeded,
// or failed for a reason other than backend. A watcher's half of up follows it.
func ConfigSyncBackendAnswered(err error) bool {
	return err == nil || ConfigSyncReason(err) != ConfigSyncReasonBackend
}

// ConfigSyncReason names why a sync failed: canceled when its context was canceled; decode or other as
// marked by ConfigSyncDecodeError and ConfigSyncApplyError, with an encoding/json syntax or type error
// read as decode; anything else came from the read of the backend.
func ConfigSyncReason(err error) string {
	var (
		marked *configSyncError
		syntax *json.SyntaxError
		typed  *json.UnmarshalTypeError
	)
	switch {
	case errors.Is(err, context.Canceled):
		return ConfigSyncReasonCanceled
	case errors.As(err, &marked):
		return marked.reason
	case errors.As(err, &syntax), errors.As(err, &typed):
		return ConfigSyncReasonDecode
	default:
		return ConfigSyncReasonBackend
	}
}
