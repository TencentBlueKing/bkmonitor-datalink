// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package service

import (
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/metrics"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/mocker"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
	"github.com/prometheus/client_golang/prometheus"
	"sync"
	"testing"
	"time"
)

func TestSloPush(t *testing.T) {
	mocker.InitTestDBConfig("../../../bmw_test.yaml")
	defer func() {
		if err := recover(); err != nil {
			logger.Errorf("SloPush Runtime panic caught: %v", err)
		}
	}()
	logger.Info("start auto SloPush task")
	var Registry = prometheus.NewRegistry()
	var bizID map[int32][]string
	bizID, _ = FindAllBiz()
	logger.Info("Biz and scenes: ", bizID)
	//fmt.Println(bizID)
	var wg sync.WaitGroup
	// 初始化注册表
	metrics.InitGauge(Registry)
	// 定义错误通道
	errChan := make(chan error, len(bizID))
	now := time.Now().Unix()
	logger.Info("Now time:", now)
	for bkBizID, scenes := range bizID {
		// 根据每个业务进行数据上报
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
				TrueSloName, TotalAlertTimeBucket, TotalSloTimeBucketDict, _ := InitStraID(int(bkBizID), scene, now)
				// 获取告警数据
				AllStrategyAggInterval := GetAllAlertTime(TotalAlertTimeBucket, TrueSloName, bkBizID)
				// 计算指标
				CalculateMetric(TotalAlertTimeBucket, TrueSloName, AllStrategyAggInterval, TotalSloTimeBucketDict, bkBizID, scene)
			}
		}()
	}
	//// 等待所有 goroutine 执行完毕
	wg.Wait()
	close(errChan)
	// 处理错误
	for err := range errChan {
		if err != nil {
			logger.Errorf("SloPush task encountered error: %v", err)
		}
	}
	//logger.Info(Registry.Gather())
	//slo.PushRes(Registry)
	// 从注册表中收集度量指标
	metricFamilies, err := Registry.Gather()
	if err != nil {
		logger.Fatalf("Could not gather metrics: %v", err)
	}

	// 遍历并打印收集到的度量指标
	for _, mf := range metricFamilies {
		fmt.Printf("Metric Family: %s\n", mf.GetName())
		for _, m := range mf.GetMetric() {
			fmt.Printf("  Metric: %v\n", m)
		}
	}

	logger.Info("auto deploy SloPush successfully")
}
