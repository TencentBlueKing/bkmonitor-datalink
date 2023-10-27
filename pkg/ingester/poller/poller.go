// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package poller

import (
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
)

// Poller 统一接口
type Poller interface {
	Pull() (define.Payload, error)
	GetInterval() int
}

var pollerRegistry = make(map[string]NewPollerFn)

type NewPollerFn func(d *define.DataSource) (Poller, error)

// NewPoller
func NewPoller(d *define.DataSource) (Poller, error) {
	plugin := d.MustGetPluginOption()
	newPollerFn, ok := pollerRegistry[plugin.PluginType]
	if !ok {
		return nil, errors.Errorf("unspported plugin type: %s", plugin.PluginType)
	}
	return newPollerFn(d)
}

// RegisterPoller
func RegisterPoller(name string, fn NewPollerFn) {
	pollerRegistry[name] = fn
}
