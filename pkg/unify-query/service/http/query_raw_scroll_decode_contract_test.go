// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and limitations under the License.

package http

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/jarcoal/httpmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/mock"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	redisUtil "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/tsdb"
)

func TestQueryRawWithScrollKeepsDecodedPrefixOnPageError(t *testing.T) {
	// Redis is a process singleton. Keep this fixture out of the package's shared
	// process, including when the complete service/http suite runs.
	const helperEnv = "UQ_SCROLL_DECODE_CONTRACT_HELPER"
	if os.Getenv(helperEnv) != "1" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestQueryRawWithScrollKeepsDecodedPrefixOnPageError$", "-test.count=1", "-test.v")
		command.Env = append(os.Environ(), helperEnv+"=1")
		output, err := command.CombinedOutput()
		require.NoError(t, err, "isolated scroll contract test failed:\n%s", output)
		return
	}

	mock.Init()
	miniRedis := miniredis.RunT(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	require.NoError(t, redisUtil.SetInstance(ctx, "scroll-decode-contract", "scroll-decode-contract", &goRedis.UniversalOptions{
		Addrs: []string{miniRedis.Addr()},
	}))
	t.Cleanup(redisUtil.Close)

	const (
		esURL     = "http://scroll-decode-contract.example.test"
		storageID = "scroll-decode-contract"
		index     = "scroll_decode_contract"
		sliceKey  = "trace.scroll|" + storageID + "|0"
	)
	tsdb.SetStorage(storageID, &tsdb.Storage{Type: metadata.ElasticsearchStorageType, Address: esURL})
	registerRawESBatchMappingResponder(t, esURL, index)
	newQuery := func() *structured.QueryTs {
		query := rawESBatchIntegrationQuery(storageID, "trace", rawESBatchIntegrationRoute{
			referenceName: "a", tableID: "trace.scroll", index: index,
		})
		query.Scroll, query.SliceMax, query.Limit = "1m", 1, 2
		return query
	}

	page := 0
	httpmock.RegisterResponder(http.MethodPost, esURL+"/_search/scroll", func(request *http.Request) (*http.Response, error) {
		var body struct {
			ScrollID string `json:"scroll_id"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			return nil, err
		}
		assert.Equal(t, "scroll-before", body.ScrollID)
		page++
		if page == 1 {
			return httpmock.NewStringResponse(http.StatusOK, `{
				"_scroll_id":"scroll-error-page","timed_out":false,
				"_shards":{"total":1,"successful":1,"failed":0},
				"hits":{"total":{"value":3,"relation":"eq"},"hits":[
					{"_index":"scroll_decode_contract","_id":"A","_source":{"trace_id":"trace","start_time":1700000000001}},
					{"_index":"scroll_decode_contract","_id":"B"}
				]}
			}`), nil
		}
		return httpmock.NewStringResponse(http.StatusOK, `{
			"_scroll_id":"scroll-after","timed_out":false,
			"_shards":{"total":1,"successful":1,"failed":0},
			"hits":{"total":{"value":3,"relation":"eq"},"hits":[
				{"_index":"scroll_decode_contract","_id":"C","_source":{"trace_id":"trace","start_time":1700000000003}}
			]}
		}`), nil
	})

	session := redisUtil.NewScrollSession("decode-contract", time.Minute, time.Second, 1, 3, 2)
	slice := session.Slice(sliceKey)
	slice.ScrollID, slice.Offset = "scroll-before", 4
	session.UpdateSliceStatus(sliceKey, slice)
	queryContext := func() context.Context {
		queryCtx := metadata.InitHashID(ctx)
		metadata.SetUser(queryCtx, &metadata.User{SpaceUID: "bkcc__2"})
		return queryCtx
	}

	total, rows, options, routes, done, err := queryRawWithScroll(queryContext(), newQuery(), session)
	require.NoError(t, err)
	assert.Zero(t, total)
	assert.False(t, done)
	assert.Empty(t, options)
	assert.Len(t, routes, 1)
	if assert.Len(t, rows, 1, "the scroll caller retains rows decoded before the page error") {
		assert.Equal(t, "A", rows[0][metadata.KeyDocID])
	}
	failed := session.Slice(sliceKey)
	assert.Equal(t, 1, failed.FailedNum)
	assert.Equal(t, redisUtil.StatusPending, failed.Status)
	assert.Equal(t, "scroll-before", failed.ScrollID)
	assert.Equal(t, 4, failed.Offset)
	stored, err := miniRedis.Get(session.SessionKey)
	require.NoError(t, err)
	var persisted redisUtil.ScrollSession
	require.NoError(t, json.Unmarshal([]byte(stored), &persisted))
	require.Contains(t, persisted.SlicesMap, sliceKey)
	require.NotNil(t, persisted.SlicesMap[sliceKey])
	assert.Equal(t, *failed, *persisted.SlicesMap[sliceKey])

	// The controlled responder returns page C on the next request; this makes
	// no guarantee that a real ES server can replay or advance the old cursor.
	total, rows, options, _, done, err = queryRawWithScroll(queryContext(), newQuery(), session)
	require.NoError(t, err)
	assert.EqualValues(t, 1, total)
	assert.False(t, done)
	if assert.Len(t, rows, 1) {
		assert.Equal(t, "C", rows[0][metadata.KeyDocID])
	}
	assert.Len(t, options, 1)
	assert.Equal(t, "scroll-after", session.Slice(sliceKey).ScrollID)
	assert.Equal(t, 6, session.Slice(sliceKey).Offset)
	assert.Equal(t, 2, page)
}
