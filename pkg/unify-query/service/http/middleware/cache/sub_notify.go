package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

func (d *Service) subLoop(ctx context.Context) {
	channelName := subscribeAll()
	// 1. 监听channel
	msgCh, closeFn := redis.Subscribe(ctx, channelName)
	if msgCh == nil {
		log.Errorf(ctx, "failed to subscribe to pattern %s", channelName)
		return
	}
	defer func() {
		// 2. 确保连接正确关闭
		if closeFn != nil {
			closeFn()
		}
	}()

	// 3. 处理所有接收到的消息
	for {
		select {
		case <-ctx.Done():
			log.Infof(ctx, "global subscription loop received context cancel signal")
			return
		case msg := <-msgCh:
			if msg != nil {
				// 3.1 从Redis频道名称中提取缓存key（使用keys.go中的通用函数）
				key := extractKeyFromChannel(msg.Channel)
				if key != "" {
					// 3.2 广播给本地等待者
					d.broadcastLocal(ctx, key)
				}
			}
		}
	}
}

func (d *Service) broadcastLocal(ctx context.Context, key string) {
	d.waiterLock.Lock()
	defer d.waiterLock.Unlock()
	// 1. 从本地waiters中查找等待者
	if wg, ok := d.waiterMap[key]; ok {
		// 2. 从map中删除，防止新的等待者加入
		delete(d.waiterMap, key)

		// 3. 原子性地获取所有channels并标记为已广播
		wg.mu.Lock()
		channels := wg.channels
		wg.channels = make([]chan struct{}, 0)
		wg.mu.Unlock()

		// 4. 关闭所有channel，唤醒等待的goroutine
		for _, ch := range channels {
			close(ch)
		}
		log.Debugf(ctx, "broadcasted to %d waiters for key: %s", len(channels), key)
	} else {
		// 5. 如果没有本地等待者，忽略此消息
		log.Debugf(ctx, "no local waiters for key: %s, ignoring message", key)
	}
}

func (d *Service) waitForNotify(ctx context.Context, key string) error {
	start := time.Now()
	timeoutCh := time.After(d.conf.executeTTL)
	select {
	// case:1  等待直到收到 channel 的关闭通知
	case <-d.waitLoop(key):
		d.metrics.recordCacheDuration("sidecar_wait", time.Since(start))
		return nil
	// case:2 超时处理
	case <-timeoutCh:
		d.metrics.recordSingleflightTimeout()
		return fmt.Errorf("timeout waiting for cache notification: %s", key)
	// case:3 上下文取消处理
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Service) waitLoop(key string) <-chan struct{} {
	d.waiterLock.Lock()
	defer d.waiterLock.Unlock()

	ch := make(chan struct{})
	var wg *WaitGroupValue

	if rev, exists := d.waiterMap[key]; !exists {
		wg = &WaitGroupValue{
			channels: []chan struct{}{ch},
		}
		d.waiterMap[key] = wg
	} else {
		wg = rev
		wg.channels = append(wg.channels, ch)
	}

	return ch
}
