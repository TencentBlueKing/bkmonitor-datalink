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

package cache

import (
	"context"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/alarm/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/api/cmdb"
)

// NewCacheManagerByType 创建缓存管理器
func NewCacheManagerByType(opt *redis.RedisOptions, prefix string, cacheType string) (Manager, error) {
	var cacheManager Manager
	var err error
	switch cacheType {
	case "host_topo":
		cacheManager, err = NewHostAndTopoCacheManager(prefix, opt)
	case "business":
		cacheManager, err = NewBusinessCacheManager(prefix, opt)
	case "module":
		cacheManager, err = NewModuleCacheManager(prefix, opt)
	case "set":
		cacheManager, err = NewSetCacheManager(prefix, opt)
	default:
		err = errors.Errorf("unsupported cache type: %s", cacheType)
	}
	return cacheManager, err
}

// RefreshAll 执行缓存管理器
func RefreshAll(ctx context.Context, cacheManager Manager) error {
	// 判断是否启用业务缓存刷新
	if cacheManager.UseBiz() {
		// 获取业务列表
		cmdbApi, err := api.GetCmdbApi()
		if err != nil {
			return errors.Wrap(err, "get cmdb api client failed")
		}
		var result cmdb.SearchBusinessResp
		_, err = cmdbApi.SearchBusiness().SetResult(&result).Request()
		if err = api.HandleApiResultError(result.ApiCommonRespMeta, err, "search business failed"); err != nil {
			return err
		}

		// 按业务刷新缓存
		for _, biz := range result.Data.Info {
			err := cacheManager.RefreshByBiz(ctx, biz.BkBizId)
			if err != nil {
				return errors.Wrapf(err, "refresh host and topo cache by biz failed, biz: %d", biz.BkBizId)
			}
		}

		// 按业务清理缓存
		for _, biz := range result.Data.Info {
			err := cacheManager.CleanByBiz(ctx, biz.BkBizId)
			if err != nil {
				return errors.Wrapf(err, "clean host and topo cache by biz failed, biz: %d", biz.BkBizId)
			}
		}
	}

	// 刷新全局缓存
	err := cacheManager.RefreshGlobal(ctx)
	if err != nil {
		return errors.Wrap(err, "refresh global host and topo cache failed")
	}

	// 清理全局缓存
	err = cacheManager.CleanGlobal(ctx)
	if err != nil {
		return errors.Wrap(err, "clean global host and topo cache failed")
	}

	return nil
}
