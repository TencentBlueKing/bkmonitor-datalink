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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/operator/common/define"
)

const (
	labelEndpointNodeName          = "__meta_kubernetes_endpoint_node_name"
	labelPodNodeName               = "__meta_kubernetes_pod_node_name"
	labelEndpointAddressTargetKind = "__meta_kubernetes_endpoint_address_target_kind"
	labelPodAddressTargetKind      = "__meta_kubernetes_pod_address_target_kind"
	labelEndpointAddressTargetName = "__meta_kubernetes_endpoint_address_target_name"
	labelPodAddressTargetName      = "__meta_kubernetes_pod_address_target_name"

	discoverTypeEndpoints = "endpoints"
)

type EndpointParams struct {
	*BaseParams
}

type Endpoint struct {
	*BaseDiscover
}

func NewEndpointDiscover(ctx context.Context, meta define.MonitorMeta, checkFn define.CheckFunc, params *EndpointParams) Discover {
	return &Endpoint{
		BaseDiscover: NewBaseDiscover(ctx, discoverTypeEndpoints, meta, checkFn, params.BaseParams),
	}
}

func (d *Endpoint) Type() string { return discoverTypeEndpoints }
func (d *Endpoint) Reload() error {
	d.Stop()
	return d.Start()
}

func (d *Endpoint) Start() error {
	d.PreStart()
	RegisterSharedDiscover(discoverTypeEndpoints, d.KubeConfig, d.getNamespaces())

	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.loopHandleTargetGroup()
	}()
	return nil
}
