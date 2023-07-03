// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package message_test

import (
	"testing"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/report/message"
)

func TestValidate(t *testing.T) {
	content := `{
		"data_id": 1500831,
		"access_token": "ae60acb57a904e51a6b9daf6252f7a4c",
		"data": [{
			"event_name": "input_your_event_name",
			"event": {
				"content": "user xxx login failed"
			},
			"target": "127.0.0.1",
			"dimension": {
				"module": "db",
				"location": "guangdong"
			},
			"timestamp": 1595840437732
		}]
	}`
	msg := message.Message{
		Kind:    "event",
		Content: content,
	}
	if err := msg.Validate(); err != nil {
		t.Fatalf("unexpected nil, but got: %+v", err)
	}
}
