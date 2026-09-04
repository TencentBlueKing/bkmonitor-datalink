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
	"sync"
	"time"

	"github.com/TencentBlueKing/bk-apigateway-sdks/core/define"
	"github.com/pkg/errors"
	"golang.org/x/time/rate"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
)

type limiterWaiter interface {
	Wait(context.Context) error
}

// RequestExecutor waits on the shared limiter before sending a CMDB request.
type RequestExecutor struct {
	timeout time.Duration
	limiter limiterWaiter
}

var (
	requestExecutorMu sync.RWMutex
	requestExecutor   *RequestExecutor
)

// NewRequestExecutor creates a request executor that throttles before the request is sent.
func NewRequestExecutor(qps float64, burst int, timeout int) *RequestExecutor {
	var limiter limiterWaiter
	if qps > 0 {
		limiter = rate.NewLimiter(rate.Limit(qps), burst)
	}

	return &RequestExecutor{
		timeout: time.Duration(timeout) * time.Second,
		limiter: limiter,
	}
}

func getRequestExecutor() *RequestExecutor {
	requestExecutorMu.RLock()
	if requestExecutor != nil {
		defer requestExecutorMu.RUnlock()
		return requestExecutor
	}
	requestExecutorMu.RUnlock()

	requestExecutorMu.Lock()
	defer requestExecutorMu.Unlock()
	if requestExecutor == nil {
		requestExecutor = NewRequestExecutor(cfg.CmdbApiRateLimitQPS, cfg.CmdbApiRateLimitBurst, cfg.CmdbApiRateLimitTimeout)
	}
	return requestExecutor
}

func operationName(op define.Operation) string {
	if op == nil {
		return "unknown_operation"
	}
	if fullName := op.FullName(); fullName != "" {
		return fullName
	}
	if name := op.Name(); name != "" {
		return name
	}
	return "unknown_operation"
}

// Wait blocks before a CMDB request is sent.
func (e *RequestExecutor) Wait(ctx context.Context, op define.Operation) error {
	if e == nil || e.limiter == nil {
		return nil
	}

	if ctx == nil {
		ctx = context.Background()
	}

	cancel := func() {}
	if e.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, e.timeout)
	}
	defer cancel()

	if err := e.limiter.Wait(ctx); err != nil {
		return errors.Wrapf(err, "wait cmdb api rate limiter for %s failed", operationName(op))
	}
	return nil
}

// DoRequest sends a CMDB request after waiting on the shared limiter.
func DoRequest(ctx context.Context, op define.Operation, result any) error {
	if err := getRequestExecutor().Wait(ctx, op); err != nil {
		return err
	}

	var err error
	if result == nil {
		_, err = op.Request()
	} else {
		_, err = op.SetResult(result).Request()
	}
	if err != nil {
		return errors.Wrapf(err, "request cmdb api %s failed", operationName(op))
	}
	return nil
}

// BatchApiRequest sends the first request to get the total count, and then sends the rest page by page.
func BatchApiRequest(ctx context.Context, pageSize int, getTotalFunc func(any) (int, error), getReqFunc func(page int) define.Operation, concurrency int) ([]any, error) {
	var resp any
	req := getReqFunc(0)
	if err := DoRequest(ctx, req, &resp); err != nil {
		return nil, errors.Wrap(err, "failed to send the first request")
	}

	total, err := getTotalFunc(resp)
	if err != nil {
		return nil, errors.Wrap(err, "failed to get the total count")
	}
	if total == 0 {
		return nil, nil
	}

	if concurrency <= 0 {
		concurrency = 1
	}

	limitChan := make(chan struct{}, concurrency)
	waitGroup := sync.WaitGroup{}
	pageCount := (total + pageSize - 1) / pageSize

	results := make([]any, pageCount)
	results[0] = resp

	errCh := make(chan error, pageCount-1)
	for p := 1; p < pageCount; p++ {
		limitChan <- struct{}{}
		waitGroup.Add(1)
		go func(page int) {
			defer func() {
				<-limitChan
				waitGroup.Done()
			}()

			var pageResp any
			if err := DoRequest(ctx, getReqFunc(page), &pageResp); err != nil {
				errCh <- errors.Wrapf(err, "failed to send the request for page %d", page)
				return
			}
			results[page] = pageResp
		}(p)
	}

	waitGroup.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			return nil, err
		}
	}

	return results, nil
}
