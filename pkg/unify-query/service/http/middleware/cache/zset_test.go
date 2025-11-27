package cache

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	goRedis "github.com/go-redis/redis/v8"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

// testSetup 设置测试环境
func testSetup(t *testing.T, limit int) (*Service, *miniredis.Miniredis) {
	config.InitConfig()

	s, err := miniredis.Run()
	require.NoError(t, err)
	t.Cleanup(s.Close)

	ctx := context.Background()
	err = redis.SetInstance(ctx, "test", &goRedis.UniversalOptions{
		Addrs: []string{s.Addr()},
	})
	require.NoError(t, err)

	client := redis.Client()
	limitKey := CacheKey(limitKeyType, "")
	_, err = client.Set(ctx, limitKey, fmt.Sprintf("%d", limit), 0).Result()
	require.NoError(t, err)

	svc := &Service{}
	err = svc.initialize(ctx)
	require.NoError(t, err)

	return svc, s
}

// TestZSet_LRU_Eviction 验证 ZSet 的 LRU 淘汰机制
func TestZSet_LRU_Eviction(t *testing.T) {
	const (
		limit      = 3
		totalItems = 5
		sleepTime  = 1 * time.Millisecond
	)

	svc, _ := testSetup(t, limit)
	ctx := context.Background()
	client := redis.Client()

	// 写入超过限制的数据项
	dataKeys := make([]string, totalItems)
	for i := 0; i < totalItems; i++ {
		dataKey := fmt.Sprintf("test_key_%d", i)
		testData := fmt.Sprintf(`{"value": "data_%d"}`, i)

		err := svc.writeRemoteCache(ctx, dataKey, []byte(testData))
		require.NoError(t, err)
		dataKeys[i] = dataKey
		time.Sleep(sleepTime) // 确保时间戳不同
	}

	// 1. 验证超出限制后 ZSet 大小维持不变
	indexKey := CacheKey(indexKeyType, "")
	// ZCard 可以根据传递进来的indexKey参数获取对应ZSet的大小
	zsetSize, err := client.ZCard(ctx, indexKey).Result()
	require.NoError(t, err)
	assert.Equal(t, int64(limit), zsetSize, "ZSet 大小应该维持在限制值")

	// 2. 验证最早的数据被直接淘汰
	// 前 totalItems - limit 个数据应该被淘汰
	evictedCount := totalItems - limit
	for i := 0; i < evictedCount; i++ {
		redisDataKey := CacheKey(dataKeyType, dataKeys[i])
		_, err := redis.Get(ctx, redisDataKey)
		assert.Equal(t, goRedis.Nil, err, "最早的数据应该被淘汰")
	}

	// 3. 验证最后保留的是最新的数据
	// 从 evictedCount 到 totalItems - 1 的数据应该存在
	for i := evictedCount; i < totalItems; i++ {
		redisDataKey := CacheKey(dataKeyType, dataKeys[i])
		data, err := redis.Get(ctx, redisDataKey)
		require.NoError(t, err)
		assert.Contains(t, data, fmt.Sprintf("data_%d", i), "最新数据应该保留")
	}
}
