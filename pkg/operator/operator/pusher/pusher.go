// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package pusher

import (
	"context"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type MetricsPusher struct {
	pusher   beat.Pusher
	disabled bool
	started  bool
}

const (
	beatName    = "operator"
	beatVersion = "1.0.0"
)

var beatConfig *beat.Config

// New 生成 MetricsPusher 实例 operator 启动不需要受到 pusher 影响
func New(ctx context.Context, disabled bool) *MetricsPusher {
	emptyPusher := &MetricsPusher{disabled: true}
	if disabled {
		return emptyPusher
	}

	var err error
	// 确保全局初始化一次 beat 即可
	if beatConfig == nil {
		beatConfig, err = beat.Init(beatName, beatVersion)
		if err != nil {
			logger.Errorf("failed to init beat config: %v", err)
			return emptyPusher
		}
	}

	pusher, err := beat.NewGsePusherWithConfig(ctx, beatConfig)
	if err != nil {
		logger.Errorf("failed to create pusher instance: %v", err)
		return emptyPusher
	}
	pusher.Gatherer(prometheus.DefaultGatherer)

	return &MetricsPusher{
		pusher:   pusher,
		disabled: false,
	}
}

func (p *MetricsPusher) StartOrUpdate(info define.ClusterInfo, podName string) {
	if p.disabled {
		logger.Info("report metrics is disabled")
		return
	}

	constLabels := map[string]string{
		"bcs_cluster_id": info.BcsClusterID,
		"bk_biz_id":      info.BizID,
		"pod_name":       podName,
		"bk_env":         info.BkEnv,
	}
	p.pusher.ConstLabels(constLabels)
	logger.Infof("update pusher const labels: %v", constLabels)
	if !p.started {
		p.pusher.StartPeriodPush()
		p.started = true
	}
}

func (p *MetricsPusher) Stop() {
	if p.disabled {
		return
	}
	p.pusher.Stop()
}
