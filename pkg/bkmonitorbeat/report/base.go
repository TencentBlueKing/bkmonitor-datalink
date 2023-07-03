// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package report

import (
	"fmt"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/report/message"
)

var senderFactor = make(map[string]NewSender)

// Sender is a interface of message sender
type Sender interface {
	Send(bkDataID int64, m *message.Message) error
	SendSync(bkDataID int64, m *message.Message) error
}

type SenderFactory struct{}

func (sf *SenderFactory) NewSender(name string, config ReportConfig) (Sender, error) {
	factory, ok := senderFactor[name]
	if !ok {
		return nil, fmt.Errorf("invalid sender name: %s", name)
	}
	return factory(config)
}

func NewSenderFactory() *SenderFactory {
	return &SenderFactory{}
}

type NewSender func(config ReportConfig) (Sender, error)

func RegisterSender(name string, sender NewSender) {
	senderFactor[name] = sender
}
