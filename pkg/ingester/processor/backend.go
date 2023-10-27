// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package processor

import (
	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
)

var backendRegistry = make(map[string]NewBackendFn)

type NewBackendFn func(d *define.DataSource) (IBackend, error)

type IBackend interface {
	// Send 将消息推送到队列中
	Send(payload define.Payload) (err error)
	Close()
}

// NewBackend 根据数据源配置创建后台
func NewBackend(d *define.DataSource) (IBackend, error) {
	newBackendFn, ok := backendRegistry[d.MQConfig.ClusterType]
	if !ok {
		return nil, errors.Errorf("unspported cluster type: %s", d.MQConfig.ClusterType)
	}
	return newBackendFn(d)
}

// RegisterBackend: 注册后台
func RegisterBackend(name string, fn NewBackendFn) {
	backendRegistry[name] = fn
}
