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
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"syscall"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

func TestTsDBRequestOutcomeClassifiesByType(t *testing.T) {
	refused := &url.Error{Op: "Post", URL: "http://192.0.2.20:8481", Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}}
	backend := func(status int) error {
		return metadata.ErrorWithCauses("backend answered", &metadata.BackendStatusError{Status: status})
	}
	cases := map[string]struct {
		err  error
		want string
	}{
		"no error":              {nil, "success"},
		"backend 503":           {backend(http.StatusServiceUnavailable), "error"},
		"backend 429":           {backend(http.StatusTooManyRequests), "error"},
		"backend 401":           {backend(http.StatusUnauthorized), "error"},
		"backend 403":           {backend(http.StatusForbidden), "error"},
		"connection refused":    {refused, "error"},
		"backend 422":           {backend(http.StatusUnprocessableEntity), "rejected"},
		"backend 400":           {backend(http.StatusBadRequest), "rejected"},
		"untyped failure":       {errors.New("decode response"), "rejected"},
		"deadline":              {fmt.Errorf("request: %w", context.DeadlineExceeded), "timeout"},
		"canceled":              {&url.Error{Op: "Post", URL: "http://192.0.2.20:8481", Err: context.Canceled}, "canceled"},
		"canceled among causes": {metadata.ErrorWithCauses("route", context.Canceled, backend(http.StatusBadGateway)), "canceled"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, c.want, TsDBRequestOutcome(c.err))
		})
	}
}

func TestTsDBRequestCellsArePrebuiltAndCounted(t *testing.T) {
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	cells := map[string]float64{}
	for _, f := range families {
		if f.GetName() != "unify_query_tsdb_requests_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			cells[labels["tsdb_type"]+"/"+labels["outcome"]] = m.GetCounter().GetValue()
		}
	}
	for _, tsdbType := range []string{"victoria_metrics", "elasticsearch", "bk_sql", "influxdb_http", "influxdb_grpc", "redis"} {
		for _, outcome := range []string{"success", "error", "rejected", "timeout", "canceled"} {
			_, ok := cells[tsdbType+"/"+outcome]
			require.True(t, ok, "%s %s", tsdbType, outcome)
		}
	}
	require.Len(t, cells, 30)

	before := cells["bk_sql/error"]
	TsDBRequestInc(context.Background(), metadata.BkSqlStorageType, metadata.ErrorWithCauses("x", &metadata.BackendStatusError{Status: http.StatusBadGateway}))
	metric, err := tsdbRequestsTotal.GetMetricWithLabelValues("bk_sql", "error")
	require.NoError(t, err)
	require.Equal(t, before+1, counterValueOf(t, metric))
}

func counterValueOf(t *testing.T, counter prometheus.Counter) float64 {
	t.Helper()
	var m dto.Metric
	require.NoError(t, counter.Write(&m))
	return m.GetCounter().GetValue()
}
