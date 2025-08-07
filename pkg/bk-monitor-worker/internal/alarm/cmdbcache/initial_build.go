// MIT License

// Copyright (c) 2021~2022 腾讯蓝鲸

// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:

// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package cmdbcache

import (
	"context"
	"fmt"
	"sync"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
)

func BuildAllInfosCache(ctx context.Context, bkTenantId, prefix string, redisOpt *redis.Options, cacheTypes []string, concurrentLimit int) error {
	bizList, err := getAllBizList(bkTenantId)
	if err != nil {
		return err
	}

	var wg sync.WaitGroup
	errChan := make(chan error)

	for _, cacheType := range cacheTypes {
		cacheManager, err := NewCacheManagerByType(bkTenantId, redisOpt, prefix, cacheType, concurrentLimit)
		if err != nil {
			return errors.Wrapf(err, "failed to create cache manager for type: %s", cacheType)
		}

		for _, bizID := range bizList {
			wg.Add(1)
			go func(ct string, bid int, cm Manager) {
				defer wg.Done()
				if err := cm.RefreshByBiz(ctx, bizID); err != nil {
					errChan <- err
					return
				}
			}(cacheType, bizID, cacheManager)
		}
	}

	wg.Wait()
	close(errChan)

	var errs []error
	for err := range errChan {
		errs = append(errs, err)
	}

	if len(errs) > 0 {
		return fmt.Errorf("some errors occurred: %v", errs)
	}

	return nil
}

func getAllBizList(bkTenantId string) ([]int, error) {
	cmdbApi := getCmdbApi(bkTenantId)

	var result cmdb.SearchBusinessResp
	_, err := cmdbApi.SearchBusiness().SetResult(&result).Request()
	if err != nil {
		return nil, errors.Wrap(err, "search business failed")
	}

	bizList := make([]int, 0, len(result.Data.Info))
	for _, biz := range result.Data.Info {
		bizList = append(bizList, biz.BkBizId)
	}

	return bizList, nil
}
