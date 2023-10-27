// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package sender

import (
	"context"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword/module"
)

const (
	EventDimensionKey = "dimension"  // 事件dimension键值
	EventEventKey     = "event"      // 事件内容的事件键值
	EventTimeStampKey = "timestamp"  // 事件事件键值
	EventEventNameKey = "event_name" // 事件名键值
	EventTargetKey    = "target"     // 监控目标键值
)

func New(ctx context.Context, cfg keyword.SendConfig, eChan chan<- define.Event) (module.Module, error) {
	// 根据配置返回实际的sender
	if cfg.OutputFormat == configs.OutputFormatEvent {
		return NewEventSender(ctx, cfg, eChan), nil
	}

	return &Sender{
		ctx:       ctx,
		cfg:       cfg,
		eventChan: eChan,
		cache:     make(map[interface{}][]string),
	}, nil
}
