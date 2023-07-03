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
	"strconv"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/confengine"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/host"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type HostConfig struct {
	HostIDPath string `config:"host_id_path"`
}

type HostInfo struct {
	IP      string
	BizID   int32
	CloudID int32
	HostID  int32
}

type Pusher interface {
	Start() error
	Stop()
}

// noopPusher 提供一个空白的 Pusher 实现
type noopPusher struct{}

func (noopPusher) Start() error {
	return nil
}

func (noopPusher) Stop() {}

func New(ctx context.Context, conf *confengine.Config) (Pusher, error) {
	var config HostConfig
	if err := conf.Unpack(&config); err != nil {
		// pusher 非关键路径 可使用空白 Pusher 代替
		logger.Warnf("unpack pusher failed, use noopPusher instead, %v", err)
		return noopPusher{}, nil
	}

	p, err := beat.NewGsePusherWithConfig(ctx, conf.RawConfig())
	if err != nil {
		return nil, err
	}
	p.Gatherer(prometheus.DefaultGatherer)

	ctx, cancel := context.WithCancel(ctx)
	return &metricsPusher{
		ctx:    ctx,
		cancel: cancel,
		pusher: p,
		watcher: host.NewWatcher(ctx, host.Config{
			HostIDPath:         config.HostIDPath,
			CMDBLevelMaxLength: 0,
			MustHostIDExist:    true,
		}),
	}, nil
}

type metricsPusher struct {
	ctx    context.Context
	cancel context.CancelFunc

	pusher   beat.Pusher
	watcher  host.Watcher
	hostInfo HostInfo
}

func (p *metricsPusher) Start() error {
	logger.Info("metricsPusher start working...")
	if err := p.watcher.Start(); err != nil {
		return err
	}

	ip := p.watcher.GetHostInnerIp()
	// ipv6 环境下可能拿不到 ip
	if ip == "" {
		logger.Warn("host watcher got empty inner ip")
	}
	p.hostInfo.IP = ip

	i, _ := strconv.Atoi(p.watcher.GetCloudId())
	p.hostInfo.CloudID = int32(i)
	p.hostInfo.BizID = int32(p.watcher.GetBizId())
	p.hostInfo.HostID = p.watcher.GetHostId()

	p.updateLabels()
	p.pusher.StartPeriodPush()

	go p.handleHostIDWatcherNotify()
	return nil
}

func (p *metricsPusher) handleHostIDWatcherNotify() {
	for {
		select {
		case <-p.watcher.Notify():
			p.updateHostInfo()
			p.updateLabels()

		case <-p.ctx.Done():
			return
		}
	}
}

func (p *metricsPusher) updateHostInfo() {
	i, err := strconv.Atoi(p.watcher.GetCloudId())
	if err == nil {
		p.hostInfo.CloudID = int32(i)
	}

	ip := p.watcher.GetHostInnerIp()
	if ip != "" {
		p.hostInfo.IP = ip
	}

	p.hostInfo.BizID = int32(p.watcher.GetBizId())
}

func (p *metricsPusher) updateLabels() {
	labels := map[string]string{
		"bk_component":    "bk-collector",
		"bk_biz_id":       strconv.Itoa(int(p.hostInfo.BizID)),
		"bk_cloud_id":     strconv.Itoa(int(p.hostInfo.CloudID)),
		"bk_host_innerip": p.hostInfo.IP,
		"bk_host_id":      strconv.Itoa(int(p.hostInfo.HostID)),
	}
	p.pusher.ConstLabels(labels)
}

func (p *metricsPusher) Stop() {
	p.cancel()
	p.watcher.Stop()
	p.pusher.Stop()
}
