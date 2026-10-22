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
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"syscall"
	"testing"

	elastic "github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// An Elasticsearch failure keeps what it was in the error, so the failed
// response is classified by ES's own status or the transport error, with the
// message text unchanged.
func TestAnElasticsearchFailureKeepsItsStatus(t *testing.T) {
	refused := &url.Error{Op: "Post", URL: "http://es.example.test:9200/_search",
		Err: &net.OpError{Op: "dial", Net: "tcp", Err: syscall.ECONNREFUSED}}
	cases := map[string]struct {
		err  error
		res  *elastic.SearchResult
		want int
	}{
		"ES answers 503":                {err: &elastic.Error{Status: http.StatusServiceUnavailable}, want: http.StatusBadGateway},
		"ES answers 500 in body":        {res: &elastic.SearchResult{Status: http.StatusInternalServerError, Error: &elastic.ErrorDetails{Type: "exception"}}, want: http.StatusBadGateway},
		"ES refuses this service (403)": {err: &elastic.Error{Status: http.StatusForbidden}, want: http.StatusBadGateway},
		"ES answers 400":                {err: &elastic.Error{Status: http.StatusBadRequest, Details: &elastic.ErrorDetails{Type: "parsing_exception"}}, want: http.StatusBadRequest},
		"ES cannot be reached":          {err: refused, want: http.StatusBadGateway},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			got := handleESError(t.Context(), "http://es.example.test:9200", c.err, c.res)
			require.Error(t, got)
			require.Equal(t, c.want, metadata.FailureStatus(got), got.Error())
		})
	}
}

// A failure fetching an index's mapping is ES's too: answering 503 there is
// the backend's, the same as on a search.
func TestAMappingFetchFailureKeepsItsStatus(t *testing.T) {
	for status, want := range map[int]int{
		http.StatusServiceUnavailable: http.StatusBadGateway,
		http.StatusForbidden:          http.StatusBadGateway,
		http.StatusBadRequest:         http.StatusBadRequest,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"error":{"type":"exception","reason":"failing"},"status":` + strconv.Itoa(status) + `}`))
			}))
			defer server.Close()
			// Its own transport: other tests in this package put httpmock on the default one.
			client, err := elastic.NewClient(elastic.SetURL(server.URL), elastic.SetSniff(false), elastic.SetHealthcheck(false),
				elastic.SetHttpClient(&http.Client{Transport: &http.Transport{}}))
			require.NoError(t, err)
			ctx, span := trace.NewSpan(t.Context(), "mapping-fetch-failure")
			_, got := resolveIndexMetadataSnapshot(ctx, span, client, "es_index")
			require.Error(t, got)
			require.Equal(t, want, metadata.FailureStatus(got), got.Error())
		})
	}
}
