package relation

import (
	"context"
	"encoding/json"
	"time"

	"github.com/go-redis/redis/v8"

	storeRedis "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const CustomRelationStatusChannel = "bkmonitorv3:entity:CustomRelationStatus:channel"

type customRelationChange struct {
	Namespace string `json:"namespace"`
	Kind      string `json:"kind"`
}

// StartCustomRelationWatcher consumes metadata notifications and refreshes only
// the affected namespace. The periodic full scan remains the recovery path for
// missed notifications and worker restarts.
func StartCustomRelationWatcher(ctx context.Context) {
	go func() {
		for {
			redisInstance := storeRedis.GetStorageRedisInstance()
			if redisInstance == nil || redisInstance.Client == nil {
				logger.Warnf("[CustomRelationWatcher] storage redis is not initialized, retrying")
				if !waitCustomRelationRetry(ctx) {
					return
				}
				continue
			}
			messages := redisInstance.Subscribe(CustomRelationStatusChannel)
			select {
			case <-ctx.Done():
				return
			case <-watchCustomRelationChannel(ctx, messages):
				logger.Warnf("[CustomRelationWatcher] redis subscription closed, retrying")
			}
			if ctx.Err() != nil {
				return
			}
			if !waitCustomRelationRetry(ctx) {
				return
			}
		}
	}()
}

func waitCustomRelationRetry(ctx context.Context) bool {
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func watchCustomRelationChannel(ctx context.Context, messages <-chan *redis.Message) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case message, ok := <-messages:
				if !ok || message == nil {
					return
				}
				var change customRelationChange
				if err := json.Unmarshal([]byte(message.Payload), &change); err != nil {
					logger.Warnf("[CustomRelationWatcher] invalid payload=%s error=%v", message.Payload, err)
					continue
				}
				if change.Kind != "CustomRelationStatus" || change.Namespace == "" {
					logger.Warnf("[CustomRelationWatcher] ignore payload=%s", message.Payload)
					continue
				}
				if err := ReportCustomRelationByNamespace(ctx, change.Namespace); err != nil {
					logger.Errorf("[CustomRelationWatcher] report namespace=%s failed: %v", change.Namespace, err)
				}
			}
		}
	}()
	return done
}
