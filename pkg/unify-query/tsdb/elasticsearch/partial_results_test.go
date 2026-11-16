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
	"testing"

	"github.com/jarcoal/httpmock"
	elastic "github.com/olivere/elastic/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
)

func TestPartialESRequestKeepsEmptyIndexProofStrict(t *testing.T) {
	mock.Init()
	ctx, count := WithPartialShardResults(context.Background())
	client, err := elastic.NewClient(elastic.SetURL(mock.EsUrl), elastic.SetSniff(false), elastic.SetHealthcheck(false))
	require.NoError(t, err)
	defer client.Stop()
	for _, tc := range []struct {
		name, response string
		wantError      bool
	}{
		{name: "complete empty index", response: `{"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":0,"relation":"eq"}}}`},
		{name: "timeout", response: `{"timed_out":true,"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":0,"relation":"eq"}}}`, wantError: true},
		{name: "partial shards", response: `{"_shards":{"total":2,"successful":1,"failed":1},"hits":{"total":{"value":0,"relation":"eq"}}}`, wantError: true},
		{name: "inexact count", response: `{"_shards":{"total":1,"successful":1,"failed":0},"hits":{"total":{"value":0,"relation":"gte"}}}`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			httpmock.RegisterResponder(http.MethodPost, mock.EsUrl+"/proof_index/_search", func(req *http.Request) (*http.Response, error) {
				assert.Equal(t, "false", req.URL.Query().Get("allow_partial_search_results"))
				return httpmock.NewStringResponse(http.StatusOK, tc.response), nil
			})
			_, queryErr := searchExactTotalHits(ctx, client, []string{"proof_index"}, elastic.NewMatchAllQuery())
			if tc.wantError {
				require.Error(t, queryErr)
			} else {
				require.NoError(t, queryErr)
			}
			assert.Zero(t, count.Load())
		})
	}
}
