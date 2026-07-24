// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 日志平台 (BlueKing - Log) available.
// Copyright (C) 2017-2021 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package controllers

import (
	"context"
	cryptorand "crypto/rand"
	"math"
	"math/big"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-log-sidecar/config"
)

// periodicReconcile 是事件和 CR Reconcile 之外的最后一道节点级兜底。
// 每轮都复用同一套 Build/Apply；无差异时不会写文件或 reload。
func (s *BkLogSidecar) periodicReconcile(ctx context.Context) {
	for {
		delay := s.nextPeriodicReconcileDelay()
		timer := time.NewTimer(delay)
		select {
		case <-timer.C:
			if err := s.generateActualBkLogConfigForPeriodicReconcile(ctx); err != nil {
				if ctx.Err() != nil {
					s.log.Info("stop periodic node configuration reconciliation")
					return
				}
				// Build/Apply 失败会保留 Last Known Good；周期循环不退出，
				// 下一轮会重新获取完整状态后继续收敛。
				s.log.Error(err, "periodic node configuration reconciliation failed",
					"nextBaseInterval", s.nextPeriodicReconcileInterval().String())
			}
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			s.log.Info("stop periodic node configuration reconciliation")
			return
		}
	}
}

func (s *BkLogSidecar) nextPeriodicReconcileDelay() time.Duration {
	interval := s.nextPeriodicReconcileInterval()
	jitter := normalizePeriodicReconcileJitter(s.periodicReconcileJitter)
	if s.periodicReconcileDelayFn != nil {
		return s.periodicReconcileDelayFn(interval, jitter)
	}
	return jitteredReconcileDelay(interval, jitter)
}

func (s *BkLogSidecar) nextPeriodicReconcileInterval() time.Duration {
	if s.periodicReconcileInterval > 0 {
		return s.periodicReconcileInterval
	}
	return config.DefaultPeriodicReconcileInterval
}

func normalizePeriodicReconcileJitter(jitter float64) float64 {
	switch {
	case jitter < 0:
		return 0
	case jitter > 1:
		return 1
	default:
		return jitter
	}
}

// jitteredReconcileDelay 在 [interval*(1-jitter), interval*(1+jitter)] 内
// 生成独立随机延迟。使用系统随机源，避免同一批节点以相同伪随机种子同步扫描。
func jitteredReconcileDelay(interval time.Duration, jitter float64) time.Duration {
	jitter = normalizePeriodicReconcileJitter(jitter)
	if interval <= 0 || jitter == 0 {
		return interval
	}

	spread := time.Duration(float64(interval) * jitter)
	if spread <= 0 {
		return interval
	}
	maximumSafeSpread := time.Duration((math.MaxInt64 - 1) / 2)
	if spread > maximumSafeSpread {
		spread = maximumSafeSpread
	}
	randomRange := big.NewInt(int64(spread)*2 + 1)
	offset, err := cryptorand.Int(cryptorand.Reader, randomRange)
	if err != nil {
		// 系统随机源极少失败；退回基础周期仍能保证补偿继续运行。
		return interval
	}
	return interval - spread + time.Duration(offset.Int64())
}
