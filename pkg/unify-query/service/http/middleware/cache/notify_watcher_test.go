package cache

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNotifyWatcher_Awakening(t *testing.T) {
	const (
		singleKeyWaiters = 10 // 同一个 key 的等待者数量
		differentKeys    = 3  // 不同 key 的数量
		waitersPerKey    = 5  // 每个 key 的等待者数量
	)

	t.Run("SingleKeySubscription", func(t *testing.T) {
		sidecar := &NotifyWatcher{
			ctx:     context.Background(),
			waiters: sync.Map{},
		}

		testKey := "single_test_key"
		var wg sync.WaitGroup
		results := make([]bool, singleKeyWaiters)

		// 假设有 singleKeyWaiters 个协程在等待同一个 key
		for i := 0; i < singleKeyWaiters; i++ {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				// 都在等待同一个 key
				ch := sidecar.waitLoop(testKey)

				select {
				case <-ch:
					results[index] = true // 成功被唤醒
				case <-time.After(1 * time.Second):
					results[index] = false // 超时未被唤醒
				}
			}(i)
		}

		// 确保所有 waiter 都已启动并在等待
		time.Sleep(100 * time.Millisecond)

		sidecar.broadcastLocal(testKey)
		wg.Wait()

		// 验证：所有 10 个协程都被唤醒（无死锁）
		for i, result := range results {
			assert.True(t, result, "协程 %d 应该被唤醒", i)
		}
	})

	t.Run("MultipleKeysAwakening", func(t *testing.T) {
		sidecar := &NotifyWatcher{
			ctx:     context.Background(),
			waiters: sync.Map{},
		}

		testKeys := []string{"key1", "key2", "key3"}
		var wg sync.WaitGroup
		successCount := 0
		var successMu sync.Mutex

		// waiter
		for _, key := range testKeys {
			for i := 0; i < waitersPerKey; i++ {
				wg.Add(1)
				go func(waitKey string) {
					defer wg.Done()
					ch := sidecar.waitLoop(waitKey)

					select {
					case <-ch:
						successMu.Lock()
						successCount++
						successMu.Unlock()
					case <-time.After(1 * time.Second):
						// 超时，未唤醒
					}
				}(key)
			}
		}

		// 确保所有 waiter 都已启动并在等待
		time.Sleep(100 * time.Millisecond)

		// 模拟收到 Notify 信号，开始唤醒本地的waiter
		for _, key := range testKeys {
			sidecar.broadcastLocal(key)
		}

		wg.Wait()

		// 验证：所有等待者都被唤醒
		assert.Equal(t, differentKeys*waitersPerKey, successCount, "所有等待者都应该被唤醒")
	})
}
