// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package discover

import (
	"context"

	promv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
)

const (
	discoverTypePod = "pod"
)

type PodParams struct {
	*BaseParams
	TLSConfig *promv1.PodMetricsEndpointTLSConfig
}

type Pod struct {
	*BaseDiscover
}

func NewPodDiscover(ctx context.Context, meta define.MonitorMeta, checkFn define.CheckFunc, params *PodParams) Discover {
	return &Pod{
		BaseDiscover: NewBaseDiscover(ctx, discoverTypePod, meta, checkFn, params.BaseParams),
	}
}

func (d *Pod) Type() string { return discoverTypePod }
func (d *Pod) Reload() error {
	d.Stop()
	return d.Start()
}

func (d *Pod) Start() error {
	d.PreStart()
	RegisterSharedDiscover(discoverTypePod, d.KubeConfig, d.getNamespaces())

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.loopHandleTargetGroup()
	}()
	return nil
}
