// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package http

import (
	"net/url"
	"time"

	"github.com/elastic/beats/libbeat/common"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/configs"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/tasks"
)

type Event struct {
	*tasks.Event
	URL           string
	Index         int
	Steps         int
	Method        string
	ResponseCode  int
	Message       string
	Charset       string
	ContentLength int
	MediaType     string
	ResolvedIP    string
}

func (e *Event) AsMapStr() common.MapStr {
	mapStr := e.Event.AsMapStr()
	mapStr["url"] = e.URL
	mapStr["steps"] = e.Steps
	mapStr["method"] = e.Method
	mapStr["response_code"] = e.ResponseCode
	mapStr["message"] = e.Message
	mapStr["charset"] = e.Charset
	mapStr["content_length"] = e.ContentLength
	mapStr["media_type"] = e.MediaType
	mapStr["resolved_ip"] = e.ResolvedIP
	return mapStr
}

// ToStep 按照采集子配置填写事件信息
func (e *Event) ToStep(index int, step *configs.HTTPTaskStepConfig, url string) {
	e.URL = url
	e.Method = step.Method
	e.Index = index
}

func (e *Event) OK() bool {
	return e.Status == define.GatherStatusOK
}

func (e *Event) Fail(code define.NamedCode) {
	e.Event.Fail(code)
	e.Status = int32(e.Index)
}

func (e *Event) FailFromError(err error) {
	e.Message = err.Error()
	switch typ := err.(type) {
	case *url.Error:
		if typ.Timeout() {
			e.Fail(define.CodeRequestTimeout)
		} else {
			e.Fail(define.CodeResponseFailed)
		}
	}
}

func NewEvent(g *Gather) *Event {
	conf := g.GetConfig().(*configs.HTTPTaskConfig)
	evt := tasks.NewEvent(g)
	evt.StartAt = time.Now()

	event := &Event{
		Event: evt,
		Steps: len(conf.Steps),
		Index: 1,
	}
	return event
}
