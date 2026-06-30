// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package cmdbcache

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
	gomonkey "github.com/agiledragon/gomonkey/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
	apiDefine "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/tenant"
)

type fakeLimiter struct {
	waitCount atomic.Int32
	waitFn    func(context.Context) error
}

func (l *fakeLimiter) Wait(ctx context.Context) error {
	l.waitCount.Add(1)
	if l.waitFn != nil {
		return l.waitFn(ctx)
	}
	return nil
}

type mockOperation struct {
	name         string
	result       any
	responseData any
	requestFn    func() (*http.Response, error)
}

func (m *mockOperation) ClientName() string { return "mock-client" }

func (m *mockOperation) Name() string { return m.name }

func (m *mockOperation) FullName() string { return m.name }

func (m *mockOperation) Apply(opts ...define.OperationOption) define.Operation {
	return m
}

func (m *mockOperation) SetHeaders(headers map[string]string) define.Operation { return m }

func (m *mockOperation) SetQueryParams(params map[string]string) define.Operation {
	return m
}

func (m *mockOperation) SetPathParams(params map[string]string) define.Operation { return m }

func (m *mockOperation) SetBodyReader(body io.Reader) define.Operation { return m }

func (m *mockOperation) SetBody(data interface{}) define.Operation { return m }

func (m *mockOperation) SetBodyProvider(provider define.BodyProvider) define.Operation {
	return m
}

func (m *mockOperation) SetResult(result interface{}) define.Operation {
	m.result = result
	return m
}

func (m *mockOperation) SetResultProvider(provider define.ResultProvider) define.Operation {
	return m
}

func (m *mockOperation) SetContext(ctx context.Context) define.Operation { return m }

func (m *mockOperation) SetContentType(contentType string) define.Operation {
	return m
}

func (m *mockOperation) SetContentLength(contentLength int64) define.Operation { return m }

func (m *mockOperation) Request() (*http.Response, error) {
	if m.requestFn != nil {
		return m.requestFn()
	}
	if dst, ok := m.result.(*any); ok {
		*dst = m.responseData
	}
	return nil, nil
}

func withRequestExecutorForTest(executor *RequestExecutor, fn func()) {
	requestExecutorMu.Lock()
	oldExecutor := requestExecutor
	requestExecutor = executor
	requestExecutorMu.Unlock()

	defer func() {
		requestExecutorMu.Lock()
		requestExecutor = oldExecutor
		requestExecutorMu.Unlock()
	}()

	fn()
}

func TestDoRequestWaitsBeforeSending(t *testing.T) {
	waitStarted := make(chan struct{})
	releaseWait := make(chan struct{})
	requestCalled := make(chan struct{}, 1)

	limiter := &fakeLimiter{
		waitFn: func(ctx context.Context) error {
			close(waitStarted)
			<-releaseWait
			return nil
		},
	}
	op := &mockOperation{
		name: "wait-before-request",
		requestFn: func() (*http.Response, error) {
			requestCalled <- struct{}{}
			return nil, nil
		},
	}

	withRequestExecutorForTest(&RequestExecutor{timeout: time.Second, limiter: limiter}, func() {
		done := make(chan error, 1)
		go func() {
			done <- DoRequest(context.Background(), op, nil)
		}()

		<-waitStarted
		select {
		case <-requestCalled:
			t.Fatal("request was sent before limiter wait completed")
		default:
		}

		close(releaseWait)
		require.NoError(t, <-done)
	})

	assert.EqualValues(t, 1, limiter.waitCount.Load())
}

func TestDoRequestReturnsTimeoutWhenLimiterBlocks(t *testing.T) {
	var requestCount atomic.Int32
	limiter := &fakeLimiter{
		waitFn: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	op := &mockOperation{
		name: "timeout-request",
		requestFn: func() (*http.Response, error) {
			requestCount.Add(1)
			return nil, nil
		},
	}

	withRequestExecutorForTest(&RequestExecutor{timeout: 10 * time.Millisecond, limiter: limiter}, func() {
		err := DoRequest(context.Background(), op, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "wait cmdb api rate limiter")
	})

	assert.EqualValues(t, 0, requestCount.Load())
	assert.EqualValues(t, 1, limiter.waitCount.Load())
}

func TestBatchApiRequestUsesSharedLimiterForEveryPage(t *testing.T) {
	limiter := &fakeLimiter{}

	withRequestExecutorForTest(&RequestExecutor{timeout: time.Second, limiter: limiter}, func() {
		results, err := BatchApiRequest(
			context.Background(),
			1,
			func(resp any) (int, error) {
				return 3, nil
			},
			func(page int) define.Operation {
				return &mockOperation{
					name:         "batch-page",
					responseData: map[string]any{"page": page},
				}
			},
			3,
		)
		require.NoError(t, err)
		require.Len(t, results, 3)
		assert.Equal(t, map[string]any{"page": 0}, results[0])
		assert.Equal(t, map[string]any{"page": 1}, results[1])
		assert.Equal(t, map[string]any{"page": 2}, results[2])
	})

	assert.EqualValues(t, 3, limiter.waitCount.Load())
}

func TestGetDynamicGroupListUsesSharedCmdbEntry(t *testing.T) {
	var getClientCalls atomic.Int32
	var batchCalls atomic.Int32

	patches := gomonkey.ApplyFunc(getCmdbApi, func(tenantId string) *cmdb.Client {
		getClientCalls.Add(1)
		return &cmdb.Client{}
	})
	defer patches.Reset()

	patches.ApplyFunc(BatchApiRequest, func(
		ctx context.Context, pageSize int, getTotalFunc func(any) (int, error), getReqFunc func(page int) define.Operation, concurrency int,
	) ([]any, error) {
		call := batchCalls.Add(1)
		switch call {
		case 1:
			return []any{cmdb.SearchDynamicGroupResp{
				ApiCommonRespMeta: apiDefine.ApiCommonRespMeta{Result: true},
				Data: cmdb.SearchDynamicGroupData{
					Count: 1,
					Info: []cmdb.SearchDynamicGroupInfo{
						{BkBizId: 2, ID: "group-1", Name: "demo-group", BkObjId: "host"},
					},
				},
			}}, nil
		case 2:
			return []any{cmdb.ExecuteDynamicGroupResp{
				ApiCommonRespMeta: apiDefine.ApiCommonRespMeta{Result: true},
				Data: cmdb.ExecuteDynamicGroupData{
					Count: 2,
					Info: []map[string]any{
						{"bk_host_id": float64(1)},
						{"bk_host_id": float64(2)},
					},
				},
			}}, nil
		default:
			return nil, nil
		}
	})

	data, err := getDynamicGroupList(context.Background(), tenant.DefaultTenantId, 2)
	require.NoError(t, err)
	require.Len(t, data, 1)
	assert.Equal(t, int32(2), batchCalls.Load())
	assert.GreaterOrEqual(t, getClientCalls.Load(), int32(2))
	assert.Equal(t, "demo-group", data["group-1"]["name"])
	assert.Equal(t, "host", data["group-1"]["bk_obj_id"])
	assert.Equal(t, []int{1, 2}, data["group-1"]["bk_inst_ids"])
}
