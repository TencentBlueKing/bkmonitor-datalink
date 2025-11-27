package cache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
)

type SideCar struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// waiters 保存本地等待者信息：key -> *WaitGroupValue
	waiters sync.Map
}

func newSideCar(ctx context.Context) *SideCar {
	childCtx, cancel := context.WithCancel(ctx)

	return &SideCar{
		ctx:     childCtx,
		cancel:  cancel,
		wg:      sync.WaitGroup{},
		waiters: sync.Map{},
	}
}

func (s *SideCar) Start() error {
	s.wg.Add(1)
	go s.subLoop()

	log.Infof(s.ctx, "optimized sidecar started with single subscription")
	return nil
}

func (s *SideCar) Stop() {
	s.cancel()
	s.wg.Wait()
	log.Infof(s.ctx, "sidecar stopped")
}

func (s *SideCar) waitLoop(key string) <-chan struct{} {
	ch := make(chan struct{})

	// 1. 尝试加载现有的等待组，如果不存在则创建新的
	if val, loaded := s.waiters.LoadOrStore(key, &WaitGroupValue{
		channels: []chan struct{}{ch},
		once:     sync.Once{},
	}); loaded {
		// 2. 如果key已存在，将当前channel添加到现有等待组
		wg := val.(*WaitGroupValue)
		wg.addChannel(ch)
	}

	return ch
}

func (s *SideCar) subLoop() {
	defer s.wg.Done()

	channelName := subscribeAll()
	// 1. 使用通配符订阅所有缓存频道（通过函数动态生成，避免硬编码）
	msgCh, closeFn := redis.Subscribe(s.ctx, channelName)
	if msgCh == nil {
		log.Errorf(s.ctx, "failed to subscribe to pattern %s", channelName)
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
		case <-s.ctx.Done():
			log.Infof(s.ctx, "global subscription loop received context cancel signal")
			return
		case msg := <-msgCh:
			if msg != nil {
				// 3.1 从Redis频道名称中提取缓存key（使用keys.go中的通用函数）
				key := extractKeyFromChannel(msg.Channel)
				if key != "" {
					// 3.2 广播给本地等待者
					s.broadcastLocal(key)
				}
			}
		}
	}
}

func (s *SideCar) broadcastLocal(key string) {
	// 1. 从本地waiters中查找等待者
	if val, ok := s.waiters.LoadAndDelete(key); ok {
		wg := val.(*WaitGroupValue)

		// 2. 使用once确保只广播一次
		wg.once.Do(func() {

			// 3. 关闭所有channel，唤醒等待的goroutine
			channels := wg.relatedChannels()
			for _, ch := range channels {
				close(ch)
			}

			log.Debugf(s.ctx, "broadcasted to %d waiters for key: %s", len(channels), key)
		})
	} else {
		// 4. 如果没有本地等待者，忽略此消息
		log.Debugf(s.ctx, "no local waiters for key: %s, ignoring message", key)
	}
}

func (d *Service) waitForNotify(ctx context.Context, key string) error {
	if d.sidecar == nil {
		return fmt.Errorf("sidecar not initialized")
	}

	start := time.Now()
	timeoutDuration := time.After(d.conf.executeTTL)
	select {
	// case:1  等待直到收到 channel 的关闭通知
	case <-d.sidecar.waitLoop(key):
		d.metrics.recordCacheDuration("sidecar_wait", time.Since(start))
		return nil
	// case:2 超时处理
	case <-timeoutDuration:
		d.metrics.recordSingleflightTimeout()
		return fmt.Errorf("timeout waiting for cache notification: %s", key)
	// case:3 上下文取消处理
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *Service) initSide() error {
	d.sidecar = newSideCar(d.ctx)
	return d.sidecar.Start()
}

func (d *Service) closeSideCard() {
	if d.sidecar != nil {
		d.sidecar.Stop()
	}
}
