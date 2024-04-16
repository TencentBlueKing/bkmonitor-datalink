// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package window

import (
	"strconv"
	"strings"

	"github.com/valyala/fastjson"
	"go.uber.org/zap"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	monitorLogger "github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type OriginMessage struct {
	DataId   int    `json:"dataid"`
	Items    []Span `json:"items"`
	Datetime string `json:"datetime"`
}

type SpanStatus struct {
	Code    core.SpanStatusCode `json:"code"`
	Message string              `json:"message"`
}

type Span struct {
	TraceId      string         `json:"trace_id"`
	ParentSpanId string         `json:"parent_span_id"`
	EndTime      int            `json:"end_time"`
	ElapsedTime  int            `json:"elapsed_time"`
	Attributes   map[string]any `json:"attributes"`
	Status       SpanStatus     `json:"status"`
	SpanName     string         `json:"span_name"`
	Resource     map[string]any `json:"resource"`
	SpanId       string         `json:"span_id"`
	Kind         int            `json:"kind"`
	StartTime    int            `json:"start_time"`
}

func ToStandardSpan(originSpan *fastjson.Value) *StandardSpan {
	standardSpan := StandardSpan{
		TraceId:      string(originSpan.GetStringBytes("trace_id")),
		SpanId:       string(originSpan.GetStringBytes("span_id")),
		SpanName:     string(originSpan.GetStringBytes("span_name")),
		ParentSpanId: string(originSpan.GetStringBytes("parent_span_id")),
		StartTime:    originSpan.GetInt("start_time"),
		EndTime:      originSpan.GetInt("end_time"),
		ElapsedTime:  originSpan.GetInt("elapsed_time"),
		StatusCode:   core.SpanStatusCode(originSpan.Get("status").GetInt("code")),
		Kind:         originSpan.GetInt("kind"),
	}
	standardSpan.Collections = exactStandardFields(standardSpan, originSpan)
	return &standardSpan
}

func exactStandardFields(standardSpan StandardSpan, originSpan *fastjson.Value) map[string]string {
	res := make(map[string]string)

	attrVal := originSpan.Get("attributes")
	resourceVal := originSpan.Get("resource")

	for _, f := range core.StandardFields {
		var valueStr string
		found := false

		switch f.Source {
		case core.SourceAttributes, core.SourceResource:
			targetVal := attrVal
			if f.Source == core.SourceResource {
				targetVal = resourceVal
			}

			if v := targetVal.Get(f.Key); v != nil {
				found = true
				switch v.Type() {
				case fastjson.TypeNumber:
					if originV, err := v.Float64(); err == nil {
						valueStr = strconv.FormatFloat(originV, 'f', -1, 64)
					}
				default:
					valueStr = strings.Trim(string(v.GetStringBytes()), `"`)
				}
			}
		case core.SourceOuter:
			found = true
			switch f.FullKey {
			case "kind":
				valueStr = strconv.Itoa(standardSpan.Kind)
			case "span_name":
				valueStr = standardSpan.SpanName
			default:
				logger.Warnf("Try to get a standard field: %s that does not exist. Is the standard field been updated?", f.Key)
				found = false
			}
		}

		if found {
			res[f.FullKey] = valueStr
		}
	}

	return res
}

func ToStandardSpanFromMapping(originSpan map[string]any) *StandardSpan {
	standardSpan := StandardSpan{
		TraceId:      originSpan["trace_id"].(string),
		SpanId:       originSpan["span_id"].(string),
		SpanName:     originSpan["span_name"].(string),
		ParentSpanId: originSpan["parent_span_id"].(string),
		StartTime:    int(originSpan["start_time"].(float64)),
		EndTime:      int(originSpan["end_time"].(float64)),
		ElapsedTime:  int(originSpan["elapsed_time"].(float64)),
		StatusCode:   core.SpanStatusCode(int(originSpan["status"].(map[string]any)["code"].(float64))),
		Kind:         int(originSpan["kind"].(float64)),
	}

	standardSpan.Collections = exactStandardFieldsFromMapping(standardSpan, originSpan)
	return &standardSpan
}

func exactStandardFieldsFromMapping(standardSpan StandardSpan, originSpan map[string]any) map[string]string {
	res := make(map[string]string)
	attrVal := originSpan["attributes"].(map[string]any)
	resourceVal := originSpan["resource"].(map[string]any)

	for _, f := range core.StandardFields {
		var valueStr string
		var found bool
		var targetVal map[string]any

		if f.Source == core.SourceAttributes || f.Source == core.SourceResource {
			targetVal = attrVal
			if f.Source == core.SourceResource {
				targetVal = resourceVal
			}

			if v, ok := targetVal[f.Key]; ok {
				found = true
				switch v := v.(type) {
				case float64:
					valueStr = strconv.FormatFloat(v, 'f', -1, 64)
				case string:
					valueStr = v
				}
			}
		} else if f.Source == core.SourceOuter {
			found = true
			switch f.FullKey {
			case "kind":
				valueStr = strconv.Itoa(standardSpan.Kind)
			case "span_name":
				valueStr = standardSpan.SpanName
			default:
				logger.Warnf("Try to get a standard field: %s that does not exist. Is the standard field been updated?", f.Key)
				found = false
			}
		}

		if found {
			res[f.FullKey] = valueStr
		}
	}

	return res
}

type CollectTrace struct {
	TraceId string
	Spans   []*StandardSpan
	Graph   *DiGraph

	Runtime Runtime
}

type StandardSpan struct {
	TraceId      string
	SpanId       string
	SpanName     string
	ParentSpanId string
	StartTime    int
	EndTime      int
	ElapsedTime  int

	StatusCode  core.SpanStatusCode
	Kind        int
	Collections map[string]string
}

func (s *StandardSpan) GetFieldValue(f ...core.CommonField) string {
	var res string
	for _, item := range f {
		res, exist := s.Collections[item.DisplayKey()]
		if exist {
			return res
		}
	}
	return res
}

// Handler window handle logic
type Handler interface {
	add(StandardSpan)
}

// Operator Window processing strategy
type Operator interface {
	Start(spanChan <-chan []StandardSpan, errorReceiveChan chan<- error, runtimeOpt ...RuntimeConfigOption)
	GetWindowsLength() int
	RecordTraceAndSpanCountMetric()
}

type Operation struct {
	Operator Operator
}

func (o *Operation) Run(spanChan <-chan []StandardSpan, errorReceiveChan chan<- error, runtimeOpt ...RuntimeConfigOption) {
	o.Operator.Start(spanChan, errorReceiveChan, runtimeOpt...)
}

// SpanExistHandler This interface determines how to process existing spans when a span received
type SpanExistHandler interface {
	handleExist(CollectTrace, StandardSpan)
}

// OperatorForm different window implements
type OperatorForm int

const (
	Distributive OperatorForm = 1 << iota
)

var logger = monitorLogger.With(
	zap.String("location", "window"),
)
