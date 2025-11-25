package cache

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/samber/lo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mockSlowQuery(key string, nodeID string) func() (interface{}, error) {
	return func() (interface{}, error) {
		time.Sleep(200 * time.Millisecond)

		return map[string]interface{}{
			"timestamp": time.Now().UnixNano(),
			"node_id":   nodeID,
			"result":    fmt.Sprintf("data_for_%s", key),
		}, nil
	}
}

func setupTestService(t *testing.T) *Service {
	ctx := context.Background()

	svc := &Service{}
	err := svc.initialize(ctx)
	if err != nil {
		require.Nil(t, err, "init err")
	}
	require.NoError(t, err)
	t.Cleanup(func() { svc.Close() })

	return svc
}

func TestDistributedSingleflight(t *testing.T) {
	service := setupTestService(t)

	key := "req:123"
	nodeIDs := []string{"node1", "node2", "node3"}

	var wg sync.WaitGroup
	results := make([]map[string]interface{}, len(nodeIDs))
	executionOrder := make([]string, len(nodeIDs))

	for i, nodeID := range nodeIDs {
		wg.Add(1)
		go func(idx int, id string) {
			defer wg.Done()

			startTime := time.Now()

			fn := mockSlowQuery(key, id)
			result, err := service.Do(key, fn)
			require.NoError(t, err)

			endTime := time.Now()
			duration := endTime.Sub(startTime)

			results[idx] = result.(map[string]interface{})
			executionOrder[idx] = fmt.Sprintf("%s completed in %v", id, duration)
		}(i, nodeID)
	}

	overallStart := time.Now()
	wg.Wait()
	overallDuration := time.Since(overallStart)

	executingNode := results[0]["node_id"]
	for i, result := range results {
		require.Equal(t, executingNode, result["node_id"],
			"result %d should be from same executing node", i)
		require.Equal(t, results[0]["timestamp"], result["timestamp"],
			"result %d should have same timestamp (single execution)", i)
	}

	require.True(t, overallDuration < 400*time.Millisecond,
		"should be fast due to singleflight, got %v", overallDuration)

	assert.Len(t, lo.Uniq(lo.Map(results, func(r map[string]interface{}, _ int) int64 {
		return r["timestamp"].(int64)
	})), 1, "所有结果的时间戳应该相同，证明只执行了一次查询")

	assert.Less(t, overallDuration, 300*time.Millisecond,
		"总耗时应该接近单次查询时间，证明Redis通知机制生效，实际耗时: %v", overallDuration)

	assert.Contains(t, nodeIDs, executingNode, "执行节点应该是模拟节点之一")
	assert.NotEmpty(t, results[0]["timestamp"], "结果应该包含有效的时间戳")
}
