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
	"flag"
	"fmt"
	"os"
	"runtime/debug"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/report/message"
)

var (
	ReportTargetTypeKey     = "report.type"
	ReportHTTPServerKey     = "report.http.server"
	ReportHTTPTokenKey      = "report.http.token"
	ReportAgentAddressKey   = "report.agent.address"
	ReportDataIDKey         = "report.bk_data_id"
	ReportMessageKindKey    = "report.message.kind"
	ReportMessageBodyKey    = "report.message.body"
	ReportEventNameKey      = "report.event.name"
	ReportEventContentKey   = "report.event.content"
	ReportEventTargetKey    = "report.event.target"
	ReportEventTimestampKey = "report.event.timestamp"
)

type ReportConfig struct {
	ReportType      string
	HTTPServer      string
	HTTPToken       string
	AgentIPCAddress string
	DataID          int64
	MessageKind     string
	MessageContent  string
	EventName       string
	EventContent    string
	EventTarget     string
	EventTimestamp  int64
}

var argsConfig = ReportConfig{}

func init() {
	flag.StringVar(&argsConfig.ReportType, ReportTargetTypeKey, "", "report type, `http` or `agent`")
	flag.StringVar(&argsConfig.HTTPServer, ReportHTTPServerKey, "", "http server address, required if report type is http")
	flag.StringVar(&argsConfig.HTTPToken, ReportHTTPTokenKey, "", "token, , required if report type is http")
	flag.StringVar(&argsConfig.AgentIPCAddress, ReportAgentAddressKey, "/var/run/ipc.state.report", "agent ipc address, default /var/run/ipc.state.report")
	flag.Int64Var(&argsConfig.DataID, ReportDataIDKey, 0, "bk_data_id, required")
	flag.StringVar(&argsConfig.MessageKind, ReportMessageKindKey, "", "message kind, event or timeseries")
	flag.StringVar(&argsConfig.MessageContent, ReportMessageBodyKey, "", "message content that will be send, json format")
	flag.StringVar(&argsConfig.EventName, ReportEventNameKey, "", "event name")
	flag.StringVar(&argsConfig.EventContent, ReportEventContentKey, "", "event content")
	flag.StringVar(&argsConfig.EventTarget, ReportEventTargetKey, "", "event target")
	flag.Int64Var(&argsConfig.EventTimestamp, ReportEventTimestampKey, 0, "event timestamp")
}

func CompareAndPrepareArgs() error {
	var err error

	// event
	if argsConfig.MessageKind == "event" {
		if len(argsConfig.MessageContent) == 0 {
			argsConfig.MessageContent = `{"data_id":0,"access_token":"","data":[{"event_name":"","metrics":{},"event":{"content":""},"target":"","dimension":{},"timestamp":0}]}`
		}
		if len(argsConfig.EventName) > 0 {
			argsConfig.MessageContent, err = sjson.Set(argsConfig.MessageContent, "data.0.event_name", argsConfig.EventName)
			if err != nil {
				return fmt.Errorf("json set event_name failed, err: %+v", err)
			}
		}
		if len(argsConfig.EventTarget) > 0 {
			argsConfig.MessageContent, err = sjson.Set(argsConfig.MessageContent, "data.0.target", argsConfig.EventTarget)
			if err != nil {
				return fmt.Errorf("json set target failed, err: %+v", err)
			}
		}
		if len(argsConfig.EventContent) > 0 {
			argsConfig.MessageContent, err = sjson.Set(argsConfig.MessageContent, "data.0.event.content", argsConfig.EventContent)
			if err != nil {
				return fmt.Errorf("json set event.content failed, err: %+v", err)
			}
		}
		if len(argsConfig.HTTPToken) > 0 {
			argsConfig.MessageContent, err = sjson.Set(argsConfig.MessageContent, "access_token", argsConfig.HTTPToken)
			if err != nil {
				return fmt.Errorf("json set access_token failed, err: %+v", err)
			}
		}
		if argsConfig.DataID != 0 {
			argsConfig.MessageContent, err = sjson.Set(argsConfig.MessageContent, "data_id", argsConfig.DataID)
			if err != nil {
				return fmt.Errorf("json set data_id field failed, err: %+v", err)
			}
		}
		if argsConfig.EventTimestamp != 0 {
			argsConfig.MessageContent, err = sjson.Set(argsConfig.MessageContent, "data.0.timestamp", argsConfig.EventTimestamp)
			if err != nil {
				return fmt.Errorf("json set timestamp field failed, err: %+v", err)
			}
		} else {
			ts := gjson.Get(argsConfig.MessageContent, "data.0.timestamp").Int()
			if ts == 0 {
				argsConfig.MessageContent, err = sjson.Set(argsConfig.MessageContent, "data.0.timestamp", time.Now().UnixNano()/1000000)
				if err != nil {
					return fmt.Errorf("json set timestamp failed, err: %+v", err)
				}
			}
		}

		return nil
	}

	// time series
	if argsConfig.MessageKind == "timeseries" {
		if argsConfig.DataID != 0 {
			argsConfig.MessageContent, err = sjson.Set(argsConfig.MessageContent, "data_id", argsConfig.DataID)
			if err != nil {
				return fmt.Errorf("json set data_id field failed, err: %+v", err)
			}
		}
	}
	return nil
}

// DoReport is enter of report package
func DoReport() error {
	if err := CompareAndPrepareArgs(); err != nil {
		return fmt.Errorf("deal arguments failed, err: %+v", err)
	}

	// make message
	msg := &message.Message{
		Kind:    argsConfig.MessageKind,
		Content: argsConfig.MessageContent,
	}

	// 捕获处理异常
	defer func() {
		if r := recover(); r != nil {
			os.Stderr.Write([]byte(msg.Content))
			debug.PrintStack()
			os.Exit(1)
		}
	}()

	if err := msg.Validate(); err != nil {
		return err
	}

	// make sender
	factory := NewSenderFactory()
	sender, err := factory.NewSender(argsConfig.ReportType, argsConfig)
	if err != nil {
		return err
	}

	// send message
	if err := sender.SendSync(argsConfig.DataID, msg); err != nil {
		return err
	}
	return nil
}
