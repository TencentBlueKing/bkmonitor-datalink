// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package task

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	cfg "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

// SloPush 上报slo数据指标
func SloPush(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("SloPush Runtime panic caught: %v", err)
		}
	}()

	if !confirmSloConfig() {
		return nil
	}

	logger.Info("start auto SloPush task")

	// 检索所有满足标签的业务
	bizID, err := service.FindAllBiz()
	if err != nil {
		logger.Errorf("find all biz_id for slo failed, %v", err)
		return err
	}
	logger.Info("Biz and scenes: ", bizID)

	// 将业务ID按批次分割，每批5个
	chunks := chunkBizID(bizID, 5)

	for _, bizChunk := range chunks {
		var wg sync.WaitGroup
		// 注册全局Registry
		sloRegistry := prometheus.NewRegistry()
		// 初始化注册表
		metrics.InitGauge(sloRegistry)
		// 获取当前时间
		now := time.Now().Unix()
		// 定义错误通道
		errChan := make(chan error, len(bizChunk))

		for bkBizID, scenes := range bizChunk {
			// 按照业务数据上报
			wg.Add(1)
			scenes := scenes
			bkBizID := bkBizID
			// 开启协程完成数据计算和注册
			go func() {
				defer wg.Done()
				// 错误处理
				defer func() {
					if err := recover(); err != nil {
						// 将错误发送到错误通道
						errChan <- fmt.Errorf("goroutine panic caught: %v", err)
					}
				}()
				for _, scene := range scenes {
					// 初始化
					trueSloName, totalAlertTimeBucket, totalSloTimeBucketDict, err := service.InitStraID(int(bkBizID), scene, now)
					if err != nil {
						logger.Errorf("slo init failed: %v", err)
						continue
					}
					// 获取告警数据
					allStrategyAggInterval := service.GetAllAlertTime(totalAlertTimeBucket, trueSloName, bkBizID, scene)
					// 计算指标
					service.CalculateMetric(totalAlertTimeBucket, trueSloName, allStrategyAggInterval,
						totalSloTimeBucketDict, bkBizID, scene)
				}
			}()
		}

		// 等待所有 goroutine 执行完毕
		wg.Wait()
		close(errChan)
		// 处理错误
		for err := range errChan {
			if err != nil {
				logger.Errorf("SloPush task encountered error: %v", err)
			}
		}
		metrics.PushRes(sloRegistry)
	}
	logger.Info("auto deploy SloPush successfully")
	return nil
}

// confirmSloConfig 判断是否开启任务
func confirmSloConfig() bool {
	if cfg.SloPushGatewayEndpoint == "" || cfg.SloPushGatewayToken == "" {
		logger.Info("Both SloPushGatewayToken and SloPushGatewayEndpoint are empty")
		return false
	} else {
		return true
	}
}

// chunkBizID 拆分
func chunkBizID(bizID map[int32][]string, size int) []map[int32][]string {
	var chunks []map[int32][]string
	keys := make([]int32, 0, len(bizID))
	for k := range bizID {
		keys = append(keys, k)
	}

	for i := 0; i < len(keys); i += size {
		end := i + size
		if end > len(keys) {
			end = len(keys)
		}
		chunk := make(map[int32][]string)
		for _, k := range keys[i:end] {
			chunk[k] = bizID[k]
		}
		chunks = append(chunks, chunk)
	}

	return chunks
}
