// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"encoding/json"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/utils"
)

type Event map[string]interface{}

type Payload struct {
	events       []Event
	PluginID     string
	DataID       int
	IgnoreResult bool
}

func (p *Payload) Clean() ([]byte, error) {
	body, err := json.Marshal(map[string]interface{}{
		"bk_data_id":     p.DataID,
		"bk_plugin_id":   p.PluginID,
		"bk_ingest_time": time.Now().Unix(),
		"data":           p.events,
	})
	if err != nil {
		return nil, err
	}
	return body, nil
}

func (p *Payload) CleanEachEvent() ([][]byte, error) {
	ingestTime := time.Now().Unix()
	var events [][]byte
	var body []byte
	var err error
	for _, event := range p.events {
		body, err = json.Marshal(map[string]interface{}{
			"bk_data_id":     p.DataID,
			"bk_plugin_id":   p.PluginID,
			"bk_ingest_time": ingestTime,
			"data":           []Event{event},
		})
		events = append(events, body)
	}
	if err != nil {
		return nil, err
	}
	return events, nil
}

func (p *Payload) AddEvents(events ...Event) {
	for _, event := range events {
		event["__bk_event_id__"] = utils.GenUUID()
	}
	p.events = append(p.events, events...)
}

func (p *Payload) GetEventCount() int {
	return len(p.events)
}
