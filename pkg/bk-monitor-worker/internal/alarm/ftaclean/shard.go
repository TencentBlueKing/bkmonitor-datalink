// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package ftaclean

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/jinzhu/gorm"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/models/resulttable"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/store/mysql"
	tc "github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const Kind = "daemon:alarm:fta-clean"

const Queue = "fta-clean"

var processSlots = sync.OnceValue(func() slots {
	slotsLimit.Set(float64(config.FTAClean.MaxConcurrency))
	return make(slots, config.FTAClean.MaxConcurrency)
})

type Operator struct{}

type Shard struct {
	Index int `json:"shard"`
}

func (Operator) GetTaskDimension(payload []byte) string { return string(payload) }

func (Operator) Start(ctx context.Context, _ chan<- error, payload []byte) {
	var shard Shard
	if !config.FTAClean.Enabled {
		return
	}
	if err := json.Unmarshal(payload, &shard); err != nil || shard.Index < 0 || shard.Index >= config.FTAClean.Shards {
		logger.Errorf("invalid FTA shard payload")
		return
	}
	processSlots() // 即使暂时没有 DataID，也对外暴露配置的进程并发上限。
	logger.Infof("FTA shard %d started", shard.Index)
	var discovery failureWindow
	configFailures := make(map[uint]*failureWindow)
	active := map[uint]*consumer{}
	defer func() {
		for _, c := range active {
			c.cancel()
		}
		for _, c := range active {
			<-c.done
		}
		logger.Infof("FTA shard %d stopped; consumers=%d", shard.Index, len(active))
	}()
	ticker := time.NewTicker(config.FTAClean.Refresh)
	defer ticker.Stop()
	for ctx.Err() == nil {
		if err := refresh(ctx, shard.Index, active, configFailures); err != nil {
			if ctx.Err() == nil {
				if count := discovery.failure(time.Now()); count > 0 {
					logger.Errorf("FTA shard %d discovery failed (attempt %d): %v", shard.Index, count, err)
				}
			}
		} else if count := discovery.recovered(); count > 0 {
			logger.Infof("FTA shard %d discovery recovered after %d failures", shard.Index, count)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type consumer struct {
	config []byte
	cancel context.CancelFunc
	done   chan struct{}
}

func refresh(ctx context.Context, index int, active map[uint]*consumer, configFailures map[uint]*failureWindow) error {
	session, err := mysql.OpenDBSession()
	if err != nil {
		return err
	}
	return reconcile(ctx, session.DB, index, active, configFailures)
}

func discoverSources(db *gorm.DB, index int) ([]resulttable.DataSource, error) {
	// 与告警后台订阅条件一致。IN 按 DataID 判断是否仍有有效实例，避免共享 DataID 时重复启动消费者。
	enabledInstances := db.Table("event_plugin_instance").Select("data_id").
		Where("is_enabled = ? AND is_deleted = ?", true, false).SubQuery()
	query := db.Where("etl_config = ? AND is_enable = ? AND MOD(bk_data_id, ?) = ?", config.FTAEvent, true, config.FTAClean.Shards, index).
		Where("bk_data_id IN (?)", enabledInstances)
	if !config.FTAClean.All {
		query = query.Where("bk_data_id IN (?)", config.FTAClean.DataIDs)
	}
	var sources []resulttable.DataSource
	err := query.Find(&sources).Error
	return sources, err
}

func reconcile(ctx context.Context, db *gorm.DB, index int, active map[uint]*consumer, configFailures map[uint]*failureWindow) error {
	sources, err := discoverSources(db, index)
	if err != nil {
		return err
	}
	seen := make(map[uint]bool, len(sources))
	for i := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		src := &sources[i]
		id := src.BkDataId
		// 已发现但暂时读不到配置时，保留正在消费的旧实例。
		seen[id] = true
		meta, err := service.NewDataSourceSvc(src).FTAConfig(db)
		old := active[id]
		stage := "read metadata"
		if err == nil {
			stage = "apply configuration"
			var raw []byte
			raw, err = json.Marshal(meta)
			if err == nil {
				err = update(ctx, active, id, raw, processSlots(), consume)
			}
		}
		if err != nil {
			w := configFailures[id]
			if w == nil {
				w = &failureWindow{}
				configFailures[id] = w
			}
			if count := w.failure(time.Now()); count > 0 {
				logger.Errorf("FTA shard %d DataID %d failed to %s (attempt %d): %v", index, id, stage, count, err)
			}
			continue
		}
		if w := configFailures[id]; w != nil {
			logger.Infof("FTA shard %d DataID %d configuration recovered after %d failures", index, id, w.count)
			delete(configFailures, id)
		}
		// 只有实际替换消费者才记录调度变化；常规刷新不产生成功日志。
		if next := active[id]; old != next {
			switch {
			case next == nil:
				logger.Infof("FTA shard %d stopped DataID %d: no enabled result table", index, id)
			case old == nil:
				logger.Infof("FTA shard %d scheduled DataID %d", index, id)
			default:
				logger.Infof("FTA shard %d reloaded DataID %d", index, id)
			}
		}
	}
	for id := range configFailures {
		if !seen[id] {
			delete(configFailures, id)
		}
	}
	for id, c := range active {
		if !seen[id] {
			c.cancel()
			<-c.done
			delete(active, id)
			logger.Infof("FTA shard %d removed DataID %d", index, id)
		}
	}
	return nil
}

func update(ctx context.Context, active map[uint]*consumer, id uint, raw []byte, capacity slots, run func(context.Context, *tc.PipelineConfig, *pipeline) error) error {
	old := active[id]
	if old != nil && bytes.Equal(old.config, raw) {
		return nil
	}
	var spec tc.PipelineConfig
	if err := json.Unmarshal(raw, &spec); err != nil {
		return err
	}
	var p *pipeline
	if len(spec.ResultTableList) > 0 {
		var err error
		p, err = compile(&spec, capacity)
		if err != nil {
			return err
		}
		if _, err = readEndpoint(spec.MQConfig); err != nil {
			return err
		}
	}
	// 先编译新规则，再等待旧消费者退出，使一个批次始终使用同一版本。
	if old != nil {
		old.cancel()
		<-old.done
		delete(active, id)
	}
	if p == nil {

		return nil
	}
	runCtx, cancel := context.WithCancel(ctx)
	c := &consumer{raw, cancel, make(chan struct{})}
	active[id] = c

	go func() {
		defer close(c.done)
		for runCtx.Err() == nil {
			err := run(runCtx, &spec, p)
			if runCtx.Err() != nil {
				return
			}
			if count := p.restarts.failure(time.Now()); count > 0 {
				logger.Errorf("FTA DataID %d consumer failed (attempt %d): %v", id, count, err)
			}
			select {
			case <-runCtx.Done():
				return
			case <-time.After(time.Second):
			}
		}
	}()
	return nil
}

func ValidateSettings() error {
	s := config.FTAClean
	if s.Shards <= 0 || s.MaxConcurrency <= 0 || s.BatchMessages <= 0 || s.BatchBytes <= 0 || s.Refresh <= 0 || s.BatchWait <= 0 {
		return fmt.Errorf("FTA shard count, concurrency, batch limits and intervals must be positive")
	}
	return nil
}

// A retry loop owns its window. Keep the first failure and one reminder per
// minute; the next successful operation reports the total and resets the window.
type failureWindow struct {
	last  time.Time
	count int
}

func (w *failureWindow) failure(now time.Time) int {
	w.count++
	if w.count > 1 && now.Sub(w.last) < time.Minute {
		return 0
	}
	w.last = now
	return w.count
}

func (w *failureWindow) recovered() int { count := w.count; *w = failureWindow{}; return count }
