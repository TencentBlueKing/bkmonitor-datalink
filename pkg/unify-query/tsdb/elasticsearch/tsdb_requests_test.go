// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	elastic "github.com/olivere/elastic/v7"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

func esRequestCount(t *testing.T, outcome string) float64 {
	t.Helper()
	families, err := prometheus.DefaultGatherer.Gather()
	require.NoError(t, err)
	for _, f := range families {
		if f.GetName() != "unify_query_tsdb_requests_total" {
			continue
		}
		for _, m := range f.GetMetric() {
			labels := map[string]string{}
			for _, l := range m.GetLabel() {
				labels[l.GetName()] = l.GetValue()
			}
			if labels["tsdb_type"] == "elasticsearch" && labels["outcome"] == outcome {
				return m.GetCounter().GetValue()
			}
		}
	}
	return -1
}

// The mapping fetch is counted with ES's own status; the index API probe before it has a fallback and is not.
func TestESMappingFetchIsCountedByOutcome(t *testing.T) {
	for status, outcome := range map[int]string{
		http.StatusServiceUnavailable: "error",
		http.StatusForbidden:          "error",
		http.StatusBadRequest:         "rejected",
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var mappingCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.Contains(r.URL.Path, "_mapping") {
					mappingCalls.Add(1)
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":{"type":"exception","reason":"failing"},"status":` + strconv.Itoa(status) + `}`))
			}))
			defer server.Close()
			client, err := elastic.NewClient(elastic.SetURL(server.URL), elastic.SetSniff(false), elastic.SetHealthcheck(false),
				elastic.SetHttpClient(&http.Client{Transport: &http.Transport{}}))
			require.NoError(t, err)
			before := map[string]float64{}
			for _, o := range []string{"success", "error", "rejected", "timeout", "canceled"} {
				before[o] = esRequestCount(t, o)
			}
			ctx, span := trace.NewSpan(t.Context(), "mapping-fetch-outcome")
			_, err = resolveIndexMetadataSnapshot(ctx, span, client, "es_index")
			require.Error(t, err)
			require.Equal(t, int32(1), mappingCalls.Load())
			for o, value := range before {
				want := value
				if o == outcome {
					want++
				}
				require.Equal(t, want, esRequestCount(t, o), "outcome %s", o)
			}
		})
	}
}
