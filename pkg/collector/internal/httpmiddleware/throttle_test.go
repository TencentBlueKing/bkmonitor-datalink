// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package httpmiddleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/throttle"
)

func TestThrottleDisabledBypassesClassifyAndMetrics(t *testing.T) {
	throttle.Stop()
	defer throttle.Stop()

	const path = "/test/httpmiddleware/throttle-disabled"
	throttle.RegisterHTTPRecordType(path, define.RecordTraces)

	nextCalled := false
	handler := Throttle("")(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
		w.WriteHeader(http.StatusAccepted)
	}))

	req := httptest.NewRequest(http.MethodPost, path, nil)
	rw := httptest.NewRecorder()
	handler.ServeHTTP(rw, req)

	assert.True(t, nextCalled)
	assert.Equal(t, http.StatusAccepted, rw.Code)
	assertMetricFamilyNotFound(t, "bk_collector_throttle_requests_total")
}

func assertMetricFamilyNotFound(t *testing.T, name string) {
	t.Helper()

	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, family := range families {
		if family.GetName() == name {
			t.Fatalf("metric family %q should not be registered", name)
		}
	}
}
