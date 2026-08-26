package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type relationRouteProviderImpl struct {
	ctx     context.Context
	cancel  context.CancelFunc
	client  goRedis.UniversalClient
	key     string
	channel string

	mu    sync.RWMutex
	ready map[string]bool
	wg    sync.WaitGroup
}

func NewRedisRelationRouteProvider(
	parentCtx context.Context, client goRedis.UniversalClient, key, channel string,
) (RelationRouteProvider, error) {
	if client == nil {
		return nil, fmt.Errorf("relation route redis client is nil")
	}
	if key == "" || channel == "" {
		return nil, fmt.Errorf("relation route redis key and channel are required")
	}
	ctx, cancel := context.WithCancel(parentCtx)
	provider := &relationRouteProviderImpl{
		ctx: ctx, cancel: cancel, client: client, key: key, channel: channel, ready: make(map[string]bool),
	}
	if err := provider.reload(); err != nil {
		cancel()
		return nil, err
	}
	provider.wg.Add(2)
	go provider.watch()
	go provider.reconcile()
	return provider, nil
}

func (p *relationRouteProviderImpl) Ready(spaceUID string) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.ready[spaceUID]
}

func (p *relationRouteProviderImpl) reload() error {
	values, err := p.client.HGetAll(p.ctx, p.key).Result()
	if err != nil {
		return err
	}
	ready := make(map[string]bool, len(values))
	for spaceUID, raw := range values {
		var route struct {
			Status string `json:"status"`
		}
		if err := json.Unmarshal([]byte(raw), &route); err != nil {
			continue
		}
		ready[spaceUID] = route.Status == "ready"
	}
	p.mu.Lock()
	p.ready = ready
	p.mu.Unlock()
	return nil
}

func (p *relationRouteProviderImpl) watch() {
	defer p.wg.Done()
	for {
		pubsub := p.client.Subscribe(p.ctx, p.channel)
		ch := pubsub.Channel()
		for {
			select {
			case <-p.ctx.Done():
				_ = pubsub.Close()
				return
			case <-ch:
				if err := p.reload(); err != nil {
					logger.Warnf("reload relation graph route failed: %s", err)
				}
			}
		}
	}
}

func (p *relationRouteProviderImpl) reconcile() {
	defer p.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			if err := p.reload(); err != nil {
				logger.Warnf("reconcile relation graph route failed: %s", err)
			}
		}
	}
}

func (p *relationRouteProviderImpl) Close() error {
	p.cancel()
	p.wg.Wait()
	return nil
}
