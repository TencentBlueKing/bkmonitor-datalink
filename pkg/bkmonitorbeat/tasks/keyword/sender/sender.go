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
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword/input/file"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks/keyword/module"
	bkcommon "github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/common"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

var CounterSend = uint64(0)

// Sender
type Sender struct {
	ctx       context.Context
	cfg       keyword.SendConfig
	eventChan chan<- define.Event
	input     <-chan interface{}
	wg        sync.WaitGroup
	cache     map[interface{}][]string
}

func (client *Sender) Start() error {
	logger.Infof("Starting sender, %s", client.ID())
	go client.run()

	return nil
}

func (client *Sender) run() error {
	tt := time.NewTicker(1 * time.Second)
	defer tt.Stop()
	for {
		select {
		case <-client.ctx.Done():
			logger.Infof("sender quit, id: %s", client.ID())
			return nil

		case <-tt.C:
			// clear cache
			for attr, buffer := range client.cache {
				client.send(buffer, attr)
			}
			client.cache = make(map[interface{}][]string)

		case event := <-client.input:
			event, err := client.cacheSend(event)
			if err != nil {
				logger.Errorf("send event error, %v", err)
				continue
			}
			// TODO 可能需要发送到进度管理
		}
	}
}

// Stop stops the input and with it all harvesters
func (client *Sender) Stop() {}

func (client *Sender) Reload(cfg interface{}) {}

func (client *Sender) Wait() {
	client.wg.Wait()
}

func (client *Sender) ID() string {
	return fmt.Sprintf("sender-%d-%s", client.cfg.DataID, client.ctx.Value("taskID").(string))
}

// AddOutput add one output
func (client *Sender) AddOutput(output chan<- interface{}) {
	// no output
}

func (client *Sender) AddInput(input <-chan interface{}) {
	client.input = input
}

func (client *Sender) cacheSend(event interface{}) (interface{}, error) {
	msg := event.(*module.LogEvent)
	attr := msg.File
	text := msg.Text

	if !client.cfg.CanPackage {
		return client.send([]string{text}, attr)
	}

	buffer, exist := client.cache[attr]
	if exist {
		// if msg count reach max count, clear cache
		if len(buffer) >= client.cfg.PackageCount {
			client.send(buffer, attr)
			// clear cache
			client.cache[attr] = []string{text}
		} else {
			client.cache[attr] = append(buffer, text)
		}
	} else {
		client.cache[attr] = []string{text}
	}
	return nil, nil
}

type OutputData struct {
	data common.MapStr
}

// IgnoreCMDBLevel :
func (o OutputData) IgnoreCMDBLevel() bool { return false }

func (o OutputData) AsMapStr() common.MapStr {
	return o.data
}

func (o OutputData) GetType() string {
	return define.ModuleKeyword
}

func (client *Sender) send(text []string, attribute interface{}) (interface{}, error) {
	filename := attribute.(*file.File).State.Source
	datetime, utctime, _ := bkcommon.GetDateTime()
	timestamp := time.Now().Unix()
	type DataItem struct {
		Iterationindex int    `config:"iterationindex"`
		Data           string `config:"data"`
	}
	var items []common.MapStr
	for index, data := range text {
		item := common.MapStr{
			"iterationindex": index,
			"data":           data,
		}
		items = append(items, item)
	}
	data := common.MapStr{
		"items":    items,
		"dataid":   client.cfg.DataID,
		"filename": filename,
		"datetime": datetime,
		"utctime":  utctime,
		"time":     timestamp,
	}
	if client.cfg.ExtMeta != nil {
		data["ext"] = client.cfg.ExtMeta
	} else {
		data["ext"] = ""
	}
	if client.cfg.GroupInfo != nil {
		data["group_info"] = client.cfg.GroupInfo
	} else {
		data["group_info"] = ""
	}
	// send data
	client.eventChan <- OutputData{data: data}
	logger.Debugf("sending %v", data)

	atomic.AddUint64(&CounterSend, 1)

	return nil, nil
}
