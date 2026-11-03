// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/observability"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/state"
)

// noDataClosePath is where the alert consumer tells this control plane a user
// closed a no-data alert (decision-024). It is served on the cluster-only
// listener (http.internal_listen) and nowhere else: the call is between
// modules of one cluster and carries no credential.
const noDataClosePath = "/internal/v1/no-data-closures"

// maxNoDataCloseBodyBytes holds the request's three fields with room to spare.
const maxNoDataCloseBodyBytes = 4 << 10

// noDataCloseRecorder is the store the route records a close in.
type noDataCloseRecorder interface {
	RecordNoDataManualClose(ctx context.Context, tenantID, strategyID, fingerprint string, at time.Time) error
}

// The route's outcomes, closed: a metric label is made of them. Each 4xx is
// its own word and its own code, so a refusal names what was wrong.
const (
	noDataCloseRecorded = "recorded"
	// noDataCloseFullSkipped is a close answered 200 and not recorded: the
	// strategy's close hash holds state.MaxNoDataManualClosesPerStrategy
	// already. The trade-off: a 503 here becomes the link's 502, which
	// refuses the user's close and is retried without end (alert-close.md,
	// "alarmd 无数据关闭联动"), so the manual close of that strategy's
	// no-data alerts would stop for a day; skipped, the link closes the
	// alert and alarmd, not knowing the absence was closed, may raise it
	// again while the data stays absent. Ten thousand closes of one
	// strategy's no-data alerts within a day is far past any user's hand;
	// the count says when it happens.
	noDataCloseFullSkipped = "full_skipped"
	// noDataCloseStoreUnavailable is the store failing (not full): 503, the
	// link answers 502 and the close is retried with the same command.
	noDataCloseStoreUnavailable = "store_unavailable"
	noDataCloseMethodNotAllowed = "method_not_allowed"
	// noDataCloseBodyUnreadable is a body that could not be read or is past
	// maxNoDataCloseBodyBytes: 400.
	noDataCloseBodyUnreadable = "body_unreadable"
	// noDataCloseBodyNotObject is a body that is not the request object:
	// not JSON, an unknown field, trailing data: 400.
	noDataCloseBodyNotObject = "body_not_object"
	// noDataCloseIdentityInvalid is a request without a tenant, a positive
	// strategy id or a 32 hex source_alert_id: 400.
	noDataCloseIdentityInvalid = "identity_invalid"
	// noDataCloseInternalListenOff is counted once at startup by a process
	// that serves the route while http.internal_listen is empty: the route
	// is then served nowhere, and every manual close of a no-data alert the
	// link sends here answers 502 there.
	noDataCloseInternalListenOff = "internal_listen_off"
)

var noDataCloseOutcomes = []string{noDataCloseRecorded, noDataCloseFullSkipped, noDataCloseStoreUnavailable, noDataCloseMethodNotAllowed,
	noDataCloseBodyUnreadable, noDataCloseBodyNotObject, noDataCloseIdentityInvalid, noDataCloseInternalListenOff}

// noDataCloseAPI serves noDataClosePath: a user closed the no-data alert
// source_alert_id of a strategy, and from the 200 on alarmd sends no alert
// for the absence that was open then. A repeat only moves the recorded time.
type noDataCloseAPI struct {
	mux   *http.ServeMux
	store noDataCloseRecorder
	now   func() time.Time
	// logger writes one line per request (noDataCloseLogStage); nil writes
	// none. Set by bindNoDataCloseAPI.
	logger *observability.Logger
	mu     sync.Mutex
	counts map[string]uint64
}

// noDataCloseLogStage is the stage of the route's lines: one per request,
// its outcome as the result. Not sampled: a request is a user's manual
// close of a no-data alert, relayed once by the link (a retry only after a
// failure), so the rate is a person's and every line can be written; which
// close was recorded, and for which strategy and alert, is then read from
// the log.
const noDataCloseLogStage = "no_data_close"

func newNoDataCloseAPI(store noDataCloseRecorder, now func() time.Time) *noDataCloseAPI {
	api := &noDataCloseAPI{mux: http.NewServeMux(), store: store, now: now, counts: map[string]uint64{}}
	api.mux.HandleFunc(noDataClosePath, api.serve)
	return api
}

func (api *noDataCloseAPI) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	api.mux.ServeHTTP(response, request)
}

func (api *noDataCloseAPI) count(outcome string) {
	api.mu.Lock()
	api.counts[outcome]++
	api.mu.Unlock()
}

// Stats is every outcome's count.
func (api *noDataCloseAPI) Stats() map[string]uint64 {
	api.mu.Lock()
	defer api.mu.Unlock()
	counts := make(map[string]uint64, len(noDataCloseOutcomes))
	for _, outcome := range noDataCloseOutcomes {
		counts[outcome] = api.counts[outcome]
	}
	return counts
}

func (api *noDataCloseAPI) serve(response http.ResponseWriter, request *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	refuse := func(status int, outcome, message string, identity ...slog.Attr) {
		api.count(outcome)
		api.logger.Warn(noDataCloseLogStage, outcome, 0, 0, append(identity, slog.Int("status", status))...)
		writeNoDataCloseError(response, status, outcome, message)
	}
	if request.Method != http.MethodPost {
		response.Header().Set("Allow", http.MethodPost)
		refuse(http.StatusMethodNotAllowed, noDataCloseMethodNotAllowed, "Only POST is served.")
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, maxNoDataCloseBodyBytes+1))
	if err != nil || len(body) > maxNoDataCloseBodyBytes {
		refuse(http.StatusBadRequest, noDataCloseBodyUnreadable, "The body could not be read or is too large.")
		return
	}
	var close struct {
		TenantID      string          `json:"bk_tenant_id"`
		StrategyID    json.RawMessage `json:"strategy_id"`
		SourceAlertID string          `json:"source_alert_id"`
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&close); err != nil || decoder.More() {
		refuse(http.StatusBadRequest, noDataCloseBodyNotObject, "The body is not the close request object.")
		return
	}
	strategyID, ok := noDataCloseStrategyID(close.StrategyID)
	if !ok || close.TenantID == "" || !state.ValidNoDataFingerprint(close.SourceAlertID) {
		refuse(http.StatusBadRequest, noDataCloseIdentityInvalid, "bk_tenant_id, a positive strategy_id and a 32 hex source_alert_id are required.")
		return
	}
	identity := []slog.Attr{slog.String("bk_tenant_id", close.TenantID), slog.String("strategy_id", strategyID),
		slog.String("source_alert_id", close.SourceAlertID)}
	err = api.store.RecordNoDataManualClose(request.Context(), close.TenantID, strategyID, close.SourceAlertID, api.now())
	switch {
	case errors.Is(err, state.ErrNoDataManualClosesFull):
		api.count(noDataCloseFullSkipped)
		api.logger.Warn(noDataCloseLogStage, noDataCloseFullSkipped, 0, 0, identity...)
	case err != nil:
		refuse(http.StatusServiceUnavailable, noDataCloseStoreUnavailable, "The close could not be recorded.",
			append(identity, slog.String("error", observability.SanitizeErrorText(err.Error())))...)
		return
	default:
		api.count(noDataCloseRecorded)
		api.logger.Info(noDataCloseLogStage, noDataCloseRecorded, 1, 0, identity...)
	}
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte("{}"))
}

// noDataCloseStrategyID accepts the strategy id as a JSON number or a
// decimal string, positive either way.
func noDataCloseStrategyID(raw json.RawMessage) (string, bool) {
	text := string(bytes.TrimSpace(raw))
	if len(text) >= 2 && text[0] == '"' && text[len(text)-1] == '"' {
		text = text[1 : len(text)-1]
	}
	id, err := strconv.ParseInt(text, 10, 64)
	if err != nil || id <= 0 {
		return "", false
	}
	return strconv.FormatInt(id, 10), true
}

func writeNoDataCloseError(response http.ResponseWriter, status int, code, message string) {
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(map[string]string{"code": code, "message": message})
}

// noDataCloseAPIOf is the route for a process with the control role; nil
// otherwise.
func noDataCloseAPIOf(store *state.ExecutionStore, now func() time.Time) *noDataCloseAPI {
	if store == nil {
		return nil
	}
	return newNoDataCloseAPI(store, now)
}

// bindNoDataCloseAPI serves the route on the internal listener and binds
// its counts. Without an internal listener the route is served nowhere:
// the link's call for every manual close of a no-data alert finds nothing
// here and the close fails there (502), so that is said once at startup
// and counted (internal_listen_off). It reports whether it was.
func bindNoDataCloseAPI(api *noDataCloseAPI, internalListen string, recorder *metric.Recorder, logger *observability.Logger,
	serve func(http.Handler)) bool {
	recorder.SetNoDataCloseAPISource(noDataCloseOutcomes, api.Stats)
	api.logger = logger
	serve(api)
	if internalListen != "" {
		return false
	}
	api.count(noDataCloseInternalListenOff)
	logger.Warn(observability.StageStartup, "no_data_close_route_unserved", 0, 0, slog.String("path", noDataClosePath),
		slog.String("detail", "http.internal_listen is empty: the alert link's manual closes of no-data alerts fail while its no_data_close_endpoint names this process"))
	return true
}
