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
	"io"
	"net/http"
	"strconv"
	"time"

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

// newNoDataCloseAPI serves noDataClosePath: a user closed the no-data alert
// source_alert_id of a strategy, and from the 200 on alarmd sends no alert
// for the absence that was open then. A repeat only moves the recorded time.
func newNoDataCloseAPI(store noDataCloseRecorder, now func() time.Time) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc(noDataClosePath, func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/json")
		response.Header().Set("Cache-Control", "no-store")
		if request.Method != http.MethodPost {
			response.Header().Set("Allow", http.MethodPost)
			writeNoDataCloseError(response, http.StatusMethodNotAllowed, "method_not_allowed", "Only POST is served.")
			return
		}
		body, err := io.ReadAll(io.LimitReader(request.Body, maxNoDataCloseBodyBytes+1))
		if err != nil || len(body) > maxNoDataCloseBodyBytes {
			writeNoDataCloseError(response, http.StatusBadRequest, "invalid_request", "The body could not be read or is too large.")
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
			writeNoDataCloseError(response, http.StatusBadRequest, "invalid_request", "The body is not the close request object.")
			return
		}
		strategyID, ok := noDataCloseStrategyID(close.StrategyID)
		if !ok || close.TenantID == "" || !state.ValidNoDataFingerprint(close.SourceAlertID) {
			writeNoDataCloseError(response, http.StatusBadRequest, "invalid_request",
				"bk_tenant_id, a positive strategy_id and a 32 hex source_alert_id are required.")
			return
		}
		if err := store.RecordNoDataManualClose(request.Context(), close.TenantID, strategyID, close.SourceAlertID, now()); err != nil {
			writeNoDataCloseError(response, http.StatusServiceUnavailable, "store_unavailable", "The close could not be recorded.")
			return
		}
		response.WriteHeader(http.StatusOK)
		_, _ = response.Write([]byte("{}"))
	})
	return mux
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
func noDataCloseAPIOf(store *state.ExecutionStore, now func() time.Time) http.Handler {
	if store == nil {
		return nil
	}
	return newNoDataCloseAPI(store, now)
}
