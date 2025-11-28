package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

func (d *Service) subLoop(ctx context.Context) {
	var (
		err error
	)
	ctx, span := trace.NewSpan(ctx, "cache-middleware-sub-loop")
	defer span.End(&err)

	channelName := subscribeAll()

	span.Set("subscribe-channel", channelName)

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
				span.Set("key", key)
				if key != "" {
					// 3.2 广播给本地等待者
					d.broadcastLocal(ctx, key)
				}
			}
		}
	}
}

func (d *Service) broadcastLocal(ctx context.Context, key string) {
	var (
		err error
	)
	ctx, span := trace.NewSpan(ctx, "cache-middleware-broadcast-local")
	defer span.End(&err)

	d.waiterLock.Lock()
	defer d.waiterLock.Unlock()
	// 1. 从本地waiters中查找等待者
	wg, existWaiter := d.waiterMap[key]

	span.Set("key", key)
	span.Set("waiter-exist", existWaiter)
	span.Set("waiter-exist-count", len(wg.channels))

	if existWaiter {
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

	}
}

func (d *Service) waitForNotify(ctx context.Context, key string) (err error) {
	ctx, span := trace.NewSpan(ctx, "cache-middleware-wait-notify")
	defer span.End(&err)

	timeoutCh := time.After(d.conf.executeTTL)

	span.Set("time-out-duration", d.conf.executeTTL.String())

	select {
	// case:1  等待直到收到 channel 的关闭通知
	case <-d.waitLoop(ctx, key):
		return nil
	// case:2 超时处理
	case <-timeoutCh:
		return fmt.Errorf("timeout waiting for cache notification: %s", key)
	// case:3 上下文取消处理
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Service) waitLoop(ctx context.Context, key string) <-chan struct{} {
	var (
		err error
	)

	ctx, span := trace.NewSpan(ctx, "cache-middleware-wait-loop")
	defer span.End(&err)

	span.Set("key", key)

	d.waiterLock.Lock()
	defer d.waiterLock.Unlock()

	ch := make(chan struct{})

	var wg *WaitGroupValue
	rev, existWaiter := d.waiterMap[key]

	if !existWaiter || rev == nil {
		wg = &WaitGroupValue{
			channels: []chan struct{}{ch},
		}
		d.waiterMap[key] = wg
	} else {
		wg = rev
		wg.channels = append(wg.channels, ch)
		existWaiter = true
	}

	span.Set("exist-waiter", existWaiter)
	var channelsCount int
	if rev != nil {
		channelsCount = len(rev.channels)
	}
	span.Set("exist-waiter-count", channelsCount)

	return ch
}
