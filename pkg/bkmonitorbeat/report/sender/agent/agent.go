// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/report"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/report/message"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/gse"
)

func Register(sleep time.Duration) {
	report.RegisterSender("agent", func(config report.ReportConfig) (report.Sender, error) {
		gseConfig := gse.Config{
			RetryTimes:    3,
			RetryInterval: 3 * time.Second,
			MsgQueueSize:  0, // direct send
			WriteTimeout:  5 * time.Second,
			Endpoint:      config.AgentIPCAddress,
			Nonblock:      false,
		}
		client, err := gse.NewGseClientFromConfig(gseConfig)
		if err != nil {
			return nil, fmt.Errorf("new gse client failed, err: %+v", err)
		}
		if err := client.Start(); err != nil {
			return nil, fmt.Errorf("start gse client failed, err: %+v", err)
		}
		// wait gse agent info
		time.Sleep(sleep)
		return &AgentSender{client: client}, nil
	})
}

type AgentSender struct {
	client *gse.GseClient
}

func (as *AgentSender) makeDynamicMsg(bkDataID int64, msg *message.Message) (*gse.GseDynamicMsg, error) {
	if bkDataID == 0 {
		return nil, errors.New("bk_data_id is empty")
	}
	content := make(map[string]interface{})
	if err := json.Unmarshal([]byte(msg.Content), &content); err != nil {
		return nil, fmt.Errorf("json unmarshal message content failed, err: %+v", err)
	}
	data, ok := content["data"]
	if !ok {
		return nil, errors.New("data field not exist")
	}
	info, err := as.client.GetAgentInfo()
	if err != nil {
		return nil, fmt.Errorf("get gse agent info failed, err: %+v", err)
	}
	// 6. 走Agent通道发送数据
	gseData := map[string]interface{}{
		"agent": map[string]interface{}{
			"type":    "bkmonitorbeat.report",
			"version": "0.0.1",
		},
		"dataid":      int32(bkDataID),
		"cloudid":     info.Cloudid,
		"bizid":       info.Bizid,
		"ip":          info.IP,
		"bk_agent_id": info.BKAgentID,
		"bk_host_id":  info.HostID,
		"version":     "",
		"data":        data,
		"bk_info":     map[string]interface{}{},
		// transfer use this field to construct indices
		"time":      time.Now().Unix(),
		"timestamp": time.Now().Unix(),
	}
	bs, err := json.Marshal(gseData)
	if err != nil {
		return nil, fmt.Errorf("json marshal message failed, err: %+v", err)
	}
	gseMsg := gse.NewGseDynamicMsg(bs, int32(bkDataID), 0, 0)
	return gseMsg, nil
}

func (as *AgentSender) SendSync(bkDataID int64, msg *message.Message) error {
	gseMsg, err := as.makeDynamicMsg(bkDataID, msg)
	if err != nil {
		return err
	}
	if err := as.client.SendSync(gseMsg); err != nil {
		return fmt.Errorf("gse send failed, err: %+v", err)
	}
	return nil
}

func (as *AgentSender) Send(bkDataID int64, msg *message.Message) error {
	gseMsg, err := as.makeDynamicMsg(bkDataID, msg)
	if err != nil {
		return err
	}
	if err := as.client.Send(gseMsg); err != nil {
		return fmt.Errorf("gse send failed, err: %+v", err)
	}
	return nil
}
