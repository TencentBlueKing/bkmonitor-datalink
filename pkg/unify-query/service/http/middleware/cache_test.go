package middleware

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/cache"
	"github.com/alicebob/miniredis/v2"
	goRedis "github.com/go-redis/redis/v8"

	"github.com/samber/lo"
	"github.com/stretchr/testify/require"
)

func TestCacheMiddleware(t *testing.T) {
	s, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(s.Close)

	rdb := goRedis.NewClient(&goRedis.Options{Addr: s.Addr()})
	t.Cleanup(func() {
		rdb.Close()
	})
	redis.SetInstance(t.Context(), "cache", &goRedis.UniversalOptions{
		Addrs: []string{s.Addr()},
	})
	svc := cache.Service{}
	err = svc.Start()
	require.NoError(t, err)
	t.Cleanup(func() {
		svc.Close()
	})

	reqCount := 5
	userCount := 3

	t.Run("多节点并发查询singleflight验证", func(t *testing.T) {
		var slowQueryExecuted int

		slowDatabaseQuery := func(userID, queryKey string) func() (interface{}, error) {
			return func() (interface{}, error) {
				slowQueryExecuted++
				time.Sleep(200 * time.Millisecond)

				return map[string]interface{}{
					"user_id":    userID,
					"query_key":  queryKey,
					"data":       fmt.Sprintf("expensive_result_for_%s_%s", userID, queryKey),
					"timestamp":  time.Now().UnixNano(),
					"call_count": slowQueryExecuted,
				}, nil
			}
		}

		var wg sync.WaitGroup
		results := make([]map[string]interface{}, 5)
		var mu sync.Mutex

		for i := 0; i < reqCount; i++ {
			wg.Add(1)
			go func(requestID int) {
				defer wg.Done()

				userID := fmt.Sprintf("user_%d", requestID%userCount)
				queryKey := "performance_metrics_2024_01"

				result, err := svc.Do(
					fmt.Sprintf("cache:%s:%s", userID, queryKey),
					slowDatabaseQuery(userID, queryKey))
				require.NoError(t, err)

				mu.Lock()
				results[requestID] = result.(map[string]interface{})
				mu.Unlock()
			}(i)
		}

		start := time.Now()
		wg.Wait()
		totalDuration := time.Since(start)

		require.Equal(t, userCount, slowQueryExecuted, "多节点singleflight失效")
		require.True(t, totalDuration < 800*time.Millisecond, "缓存性能优化失效")
		userReqTimeStampMap := lo.Reduce(results, func(acc map[string]int64, res map[string]interface{}, _ int) map[string]int64 {
			uid := res["user_id"].(string)
			tmp := res["timestamp"].(int64)
			if timeStamp, ok := acc[uid]; ok {
				require.Equal(t, timeStamp, tmp, "同一用户多次请求未命中缓存")
			} else {
				acc[uid] = tmp
			}
			return acc
		}, map[string]int64{})
		require.Equal(t, userCount, len(userReqTimeStampMap), "用户请求结果数异常")
	})
}
