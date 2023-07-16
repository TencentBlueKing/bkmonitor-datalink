// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package bkpipe_multi

import (
	"github.com/elastic/beats/libbeat/beat"
	"github.com/elastic/beats/libbeat/common"
	"github.com/elastic/beats/libbeat/logp"
	"github.com/elastic/beats/libbeat/outputs"
	"github.com/elastic/beats/libbeat/outputs/outest"
	"github.com/elastic/beats/libbeat/publisher"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/output/gse"
)

// Output : gse output, for libbeat output
type Output struct {
	gse.Output
	im    outputs.IndexManager
	beat  beat.Info
	stats outputs.Observer
}

func init() {
	outputs.RegisterType("bkpipe_multi", MakeBKPipeMulti)
	outputs.RegisterType("bkpipe_multi_ignore", MakeBKPipeMultiWithoutCheckConn)
}

// MakeBKPipeMulti create gse output
// compatible with old configurations
func MakeBKPipeMulti(im outputs.IndexManager, beat beat.Info, stats outputs.Observer, cfg *common.Config) (outputs.Group, error) {
	group, err := gse.MakeGSE(im, beat, stats, cfg)
	if err != nil {
		return group, err
	}
	output := &Output{
		Output: *group.Clients[0].(*gse.Output),
		im:     im,
		beat:   beat,
		stats:  stats,
	}
	return outputs.Success(group.BatchSize, group.Retry, output)
}

// MakeBKPipeMultiWithoutCheckConn create gse output without check connection
func MakeBKPipeMultiWithoutCheckConn(im outputs.IndexManager, beat beat.Info, stats outputs.Observer, cfg *common.Config) (outputs.Group, error) {
	group, err := gse.MakeGSEWithoutCheckConn(im, beat, stats, cfg)
	if err != nil {
		return group, err
	}
	output := &Output{
		Output: *group.Clients[0].(*gse.Output),
		im:     im,
		beat:   beat,
		stats:  stats,
	}
	return outputs.Success(group.BatchSize, group.Retry, output)
}

// String returns the name of the output client
func (c *Output) String() string {
	return "bkpipe_multi"
}

// Publish implement output interface
func (c *Output) Publish(batch publisher.Batch) error {

	events := batch.Events()
	var eventsToReport []beat.Event
	for i := range events {
		if events[i].Content.Fields == nil {
			gse.MetricGsePublishDropped.Add(1)
			continue
		}

		// 检测dataid是否存在
		content := events[i].Content
		data := content.Fields
		val, err := data.GetValue("dataid")
		if err != nil {
			logp.Err("event lost dataid field, %v", err)
			gse.MetricGsePublishDropped.Add(1)
			continue
		}

		dataid := c.GetDataID(val)
		if dataid <= 0 {
			logp.Err("dataid %d <= 0", dataid)
			gse.MetricGsePublishDropped.Add(1)
			continue
		}

		// Meta 信息比较冗余，先去掉
		//if content.Meta != nil {
		//	data.Put("@meta", content.Meta)
		//}

		data, err = c.AddEventAttachInfo(dataid, data)

		if err != nil {
			logp.Err("add event attach info failed, %v", err)
			gse.MetricGsePublishDropped.Add(1)
			continue
		}

		events[i].Content.Fields = data

		gse.MetricGsePublishReceived.Add(1)
		eventsToReport = append(eventsToReport, events[i].Content)
	}

	groups := GroupEventsByOutput(eventsToReport)

	for outputHash, clientEvents := range groups {
		if outputHash == GseOutputGroupName {
			c.publishGseEvents(clientEvents)
		} else {
			client, err := LoadOutputClient(outputHash, c.im, c.beat, c.stats)
			if client == nil {
				if err != nil {
					logp.Err("load output client error, %v", err)
				}
			} else {
				c.publishCustomOutputEvents(clientEvents, client)
			}
		}
	}

	batch.ACK()
	return nil
}

// publishCustomOutputEvents 发布自定义output的事件
func (c *Output) publishCustomOutputEvents(events []beat.Event, client outputs.Client) {
	err := client.Publish(outest.NewBatch(events...))
	if err != nil {
		logp.Err("publish event failed: %v", err)
		gse.MetricGsePublishFailed.Add(int64(len(events)))
	} else {
		gse.MetricGsePublishTotal.Add(int64(len(events)))
	}
}

// publishGseEvents 发布 gse 事件
func (c *Output) publishGseEvents(events []beat.Event) {
	for i := range events {
		data := events[i].Fields
		val, _ := data.GetValue("dataid")
		dataID := c.GetDataID(val)
		err := c.ReportCommonData(dataID, data)

		if err != nil {
			logp.Err("publish event failed: %v", err)
			gse.MetricGsePublishFailed.Add(1)
		} else {
			gse.MetricGsePublishTotal.Add(1)
		}
	}
}

// Close gse client and all other outputs will be closed
func (c *Output) Close() error {
	CloseOutputClients()
	return c.Output.Close()
}
