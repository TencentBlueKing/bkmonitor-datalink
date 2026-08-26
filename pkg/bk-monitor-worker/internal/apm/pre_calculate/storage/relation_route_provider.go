package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	goRedis "github.com/go-redis/redis/v8"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type builtInRelationDetail struct {
	BkTenantID string `json:"bk_tenant_id"`
	BkBizID    string `json:"bk_biz_id"`
	TableID    string `json:"table_id"`
	Token      string `json:"token"`
}

type relationRouteProviderImpl struct {
	ctx    context.Context
	cancel context.CancelFunc
	client goRedis.UniversalClient

	resultTableDetailKey           string
	resultTableDetailChannel       string
	resultTableDetailDeleteChannel string
	builtInDetailKey               string
	builtInDetailChannel           string
	enableMultiTenant              bool

	mu    sync.RWMutex
	ready map[string]bool
	wg    sync.WaitGroup
}

func NewRedisRelationRouteProvider(
	parentCtx context.Context,
	client goRedis.UniversalClient,
	resultTableDetailKey string,
	resultTableDetailChannel string,
	resultTableDetailDeleteChannel string,
	builtInDetailKey string,
	builtInDetailChannel string,
	enableMultiTenant bool,
) (RelationRouteProvider, error) {
	if client == nil {
		return nil, fmt.Errorf("relation route redis client is nil")
	}
	if resultTableDetailKey == "" || resultTableDetailChannel == "" || resultTableDetailDeleteChannel == "" ||
		builtInDetailKey == "" || builtInDetailChannel == "" {
		return nil, fmt.Errorf("relation route redis keys and channels are required")
	}
	ctx, cancel := context.WithCancel(parentCtx)
	provider := &relationRouteProviderImpl{
		ctx:                            ctx,
		cancel:                         cancel,
		client:                         client,
		resultTableDetailKey:           resultTableDetailKey,
		resultTableDetailChannel:       resultTableDetailChannel,
		resultTableDetailDeleteChannel: resultTableDetailDeleteChannel,
		builtInDetailKey:               builtInDetailKey,
		builtInDetailChannel:           builtInDetailChannel,
		enableMultiTenant:              enableMultiTenant,
		ready:                          make(map[string]bool),
	}
	if err := provider.reload(); err != nil {
		logger.Warnf("initial relation route load failed: %s", err)
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
	builtInDetails, err := p.client.HGetAll(p.ctx, p.builtInDetailKey).Result()
	if err != nil {
		return err
	}
	ready := make(map[string]bool, len(builtInDetails))
	for spaceUID, raw := range builtInDetails {
		detail, err := parseBuiltInRelationDetail(spaceUID, raw)
		if err != nil || detail.Token == "" || detail.TableID == "" {
			continue
		}
		redisTableID := detail.TableID
		if p.enableMultiTenant && detail.BkTenantID != "" {
			redisTableID = fmt.Sprintf("%s|%s", redisTableID, detail.BkTenantID)
		}
		rawRoute, err := p.client.HGet(p.ctx, p.resultTableDetailKey, redisTableID).Result()
		if err != nil {
			if err != goRedis.Nil {
				logger.Warnf("load relation result table route failed, tableID=%s: %s", redisTableID, err)
			}
			continue
		}
		ready[spaceUID] = hasSurrealDBRoute(rawRoute)
	}
	p.mu.Lock()
	p.ready = ready
	p.mu.Unlock()
	return nil
}

func parseBuiltInRelationDetail(spaceUID, raw string) (builtInRelationDetail, error) {
	var detail builtInRelationDetail
	if err := json.Unmarshal([]byte(raw), &detail); err != nil {
		return detail, err
	}
	if detail.BkBizID == "" && strings.HasPrefix(spaceUID, "bkcc__") {
		detail.BkBizID = strings.TrimPrefix(spaceUID, "bkcc__")
	}
	if detail.BkTenantID == "" {
		detail.BkTenantID = "system"
	}
	if detail.TableID == "" && detail.BkBizID != "" {
		detail.TableID = fmt.Sprintf("%s_bkcc_built_in_time_series.__default__", detail.BkBizID)
	}
	return detail, nil
}

func hasSurrealDBRoute(raw string) bool {
	var route map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &route); err != nil {
		return false
	}
	var storageType string
	if value, ok := route["storage_type"]; ok {
		_ = json.Unmarshal(value, &storageType)
	}
	if storageType == "surrealdb" {
		return validSurrealDBRoute(route)
	}
	var nested map[string]json.RawMessage
	if value, ok := route["surrealdb"]; !ok || json.Unmarshal(value, &nested) != nil {
		return false
	}
	return validSurrealDBRoute(nested)
}

func validSurrealDBRoute(route map[string]json.RawMessage) bool {
	var storageID uint
	if value, ok := route["storage_id"]; !ok || json.Unmarshal(value, &storageID) != nil || storageID == 0 {
		return false
	}
	var namespace string
	if value, ok := route["namespace"]; !ok || json.Unmarshal(value, &namespace) != nil || namespace == "" {
		return false
	}
	var database string
	if value, ok := route["database"]; ok {
		_ = json.Unmarshal(value, &database)
	}
	if database == "" {
		if value, ok := route["db"]; ok {
			_ = json.Unmarshal(value, &database)
		}
	}
	var clusterName string
	if value, ok := route["cluster_name"]; ok {
		_ = json.Unmarshal(value, &clusterName)
	}
	if clusterName == "" {
		if value, ok := route["storage_name"]; ok {
			_ = json.Unmarshal(value, &clusterName)
		}
	}
	return database != "" && clusterName != ""
}

func (p *relationRouteProviderImpl) watch() {
	defer p.wg.Done()
	channels := []string{
		p.resultTableDetailChannel,
		p.resultTableDetailDeleteChannel,
		p.builtInDetailChannel,
	}
	for {
		pubsub := p.client.Subscribe(p.ctx, channels...)
		ch := pubsub.Channel()
	messageLoop:
		for {
			select {
			case <-p.ctx.Done():
				_ = pubsub.Close()
				return
			case _, ok := <-ch:
				if !ok {
					_ = pubsub.Close()
					break messageLoop
				}
				if err := p.reload(); err != nil {
					logger.Warnf("reload relation route failed: %s", err)
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
				logger.Warnf("reconcile relation route failed: %s", err)
			}
		}
	}
}
