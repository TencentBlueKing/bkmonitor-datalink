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
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	elastic "github.com/olivere/elastic/v7"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
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

func esRequestCounts(t *testing.T) map[string]float64 {
	t.Helper()
	counts := map[string]float64{}
	for _, o := range []string{"success", "error", "rejected", "timeout", "canceled", "body_error"} {
		counts[o] = esRequestCount(t, o)
	}
	return counts
}

func requireOneESRequest(t *testing.T, before map[string]float64, outcome string) {
	t.Helper()
	for o, value := range before {
		want := value
		if o == outcome {
			want++
		}
		require.Equal(t, want, esRequestCount(t, o), "outcome %s", o)
	}
}

// An _msearch request is counted once with its final outcome, after the body is checked: an answer that
// does not parse or holds another number of responses is a failure of the backend, and a member that
// failed is a failure stated in the body. What the caller gets is classified the same way as a single
// search: by ES's status, the whole batch's or the member's.
func TestESRawBatchIsCountedAfterTheBodyCheck(t *testing.T) {
	const ok = `{"hits":{"total":{"value":0,"relation":"eq"},"hits":[]}}`
	failed := func(status int, errType string) string {
		return `{"status":` + strconv.Itoa(status) + `,"error":{"type":"` + errType + `","reason":"failing"}}`
	}
	for name, c := range map[string]struct {
		status  int
		body    string
		outcome string
		batch   int // FailureStatus of the batch's error; 0 when the batch answered
		members []int
	}{
		"answered":            {http.StatusOK, `{"responses":[` + ok + `,` + ok + `]}`, "success", 0, []int{0, 0}},
		"count mismatch":      {http.StatusOK, `{"responses":[` + ok + `]}`, "error", http.StatusBadGateway, nil},
		"undecodable":         {http.StatusOK, `{"responses":[`, "error", http.StatusBadGateway, nil},
		"member unavailable":  {http.StatusOK, `{"responses":[` + ok + `,` + failed(503, "exception") + `]}`, "body_error", 0, []int{0, http.StatusBadGateway}},
		"member rejected":     {http.StatusOK, `{"responses":[` + failed(400, "parsing_exception") + `,` + ok + `]}`, "body_error", 0, []int{http.StatusBadRequest, 0}},
		"member bucket limit": {http.StatusOK, `{"responses":[` + failed(503, "too_many_buckets_exception") + `,` + ok + `]}`, "body_error", 0, []int{http.StatusBadRequest, 0}},
		"batch unavailable":   {http.StatusServiceUnavailable, failed(503, "exception"), "error", http.StatusBadGateway, nil},
		"batch rejected":      {http.StatusBadRequest, failed(400, "parsing_exception"), "rejected", http.StatusBadRequest, nil},
	} {
		t.Run(name, func(t *testing.T) {
			connect, instance, server := rawBatchTestServer(t, c.status, c.body)
			defer server.Close()
			batch := rawBatchFromMembers(t, []RawBatchMember{
				{Ordinal: 0, Prepared: rawBatchPrepared(t, connect, []string{"first"}, `{}`, 0)},
				{Ordinal: 1, Prepared: rawBatchPrepared(t, connect, []string{"second"}, `{}`, 0)},
			})
			before := esRequestCounts(t)
			results, err := instance.ExecuteRawBatch(context.Background(), batch, 0)
			requireOneESRequest(t, before, c.outcome)
			if c.batch != 0 {
				require.Error(t, err)
				require.Equal(t, c.batch, metadata.FailureStatus(err))
				return
			}
			require.NoError(t, err)
			require.Len(t, results, len(c.members))
			for index, want := range c.members {
				if want == 0 {
					require.NoError(t, results[index].Err)
					continue
				}
				require.Error(t, results[index].Err)
				require.Equal(t, want, metadata.FailureStatus(results[index].Err), "member %d", index)
			}
		})
	}
}

// The caller's deadline passing while _msearch waits is a timeout, for the count and for the caller.
func TestESRawBatchDeadlineIsATimeout(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer server.Close()
	defer close(release)
	connect := Connect{Address: server.URL}
	instance := rawBatchInstance(t, connect)
	batch := rawBatchFromMembers(t, []RawBatchMember{
		{Ordinal: 0, Prepared: rawBatchPrepared(t, connect, []string{"first"}, `{}`, 0)},
		{Ordinal: 1, Prepared: rawBatchPrepared(t, connect, []string{"second"}, `{}`, 0)},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	before := esRequestCounts(t)
	_, err := instance.ExecuteRawBatch(ctx, batch, 0)
	require.Error(t, err)
	require.Equal(t, http.StatusGatewayTimeout, metadata.FailureStatus(err))
	requireOneESRequest(t, before, "timeout")
}
