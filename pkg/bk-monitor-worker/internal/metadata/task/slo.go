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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/metadata/service"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	t "github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/task"
)

// SloPush 上报slo数据指标
func SloPush(ctx context.Context, t *t.Task) error {
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("SloPush Runtime panic caught: %v", err)
		}
	}()

	logger.Info("start auto SloPush task")

	//注册全局Registry
	var Registry = prometheus.NewRegistry()

	//检索所有满足标签的业务
	bizID, err := service.FindAllBiz()
	if err != nil {
		logger.Errorf("find all biz_id for slo failed, %v", err)
	}
	logger.Info("Biz and scenes: ", bizID)
	var wg sync.WaitGroup
	//初始化注册表
	metrics.InitGauge(Registry)
	//获取当前时间
	now := time.Now().Unix()
	logger.Info("Now time:", now)
	// 定义错误通道
	errChan := make(chan error, len(bizID))
	for bkBizID, scenes := range bizID {
		//按照业务数据上报
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
				TrueSloName, TotalAlertTimeBucket, TotalSloTimeBucketDict, err := service.InitStraID(int(bkBizID), scene, now)
				if err != nil {
					logger.Errorf("slo init failed: %v", err)
					continue
				}
				// 获取告警数据
				AllStrategyAggInterval := service.GetAllAlertTime(TotalAlertTimeBucket, TrueSloName, bkBizID, scene)
				// 计算指标
				service.CalculateMetric(TotalAlertTimeBucket, TrueSloName, AllStrategyAggInterval,
					TotalSloTimeBucketDict, bkBizID, scene)
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
	metricFamilies, err := Registry.Gather()
	if err != nil {
		logger.Errorf("Could not gather metrics: %v", err)
	}

	// 遍历并打印收集到的度量指标
	for _, mf := range metricFamilies {
		logger.Infof("Metric Family: %s", mf.GetName())
		for _, m := range mf.GetMetric() {
			logger.Infof("  Metric: %v", m)
		}
	}
	metrics.PushRes(Registry)
	logger.Info("auto deploy SloPush successfully")
	return nil
}
