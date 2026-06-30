// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package datadogrum

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"runtime/debug"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

type DatadogEvent struct {
	Type string                 `json:"type"`
	Data map[string]interface{} `json:"data,omitempty"`
	Meta map[string]interface{} `json:"meta,omitempty"`
}

// DatadogEventV2 表示 Datadog RUM V2 格式的事件结构
// 保留完整的 Datadog RUM 事件属性
type DatadogEventV2 struct {
	// 基本字段
	Type      string `json:"type"`
	EventType string `json:"event_type,omitempty"`
	Date      int64  `json:"date"`

	// 顶层属性
	Source  string `json:"source,omitempty"`  // browser, ios, android
	Service string `json:"service,omitempty"` // 服务名称
	Version string `json:"version,omitempty"` // 版本号
	DDTags  string `json:"ddtags,omitempty"`  // 标签字符串

	// 嵌套对象（保持为 map 以支持动态结构）
	DD           *DDData                `json:"_dd,omitempty"`          // Datadog 元数据（强类型结构）
	Application  map[string]interface{} `json:"application,omitempty"`  // 应用信息
	View         *ViewData              `json:"view,omitempty"`         // 视图信息（强类型结构）
	Session      map[string]interface{} `json:"session,omitempty"`      // 会话信息
	Connectivity map[string]interface{} `json:"connectivity,omitempty"` // 网络连接信息
	User         map[string]interface{} `json:"usr,omitempty"`          // 用户信息
	Display      map[string]interface{} `json:"display,omitempty"`      // 显示信息
	Resource     *ResourceData          `json:"resource,omitempty"`     // 资源详细信息（强类型结构）
	Error        map[string]interface{} `json:"error,omitempty"`        // 错误信息
	Action       map[string]interface{} `json:"action,omitempty"`       // 用户操作信息
	LongTask     map[string]interface{} `json:"long_task,omitempty"`    // 长任务信息

	// 兼容旧字段（用于特殊场景）
	Data    map[string]interface{} `json:"data,omitempty"`
	Meta    map[string]interface{} `json:"meta,omitempty"`
	Context map[string]interface{} `json:"context,omitempty"`
}

// ResourceTiming 资源时间信息（用于 dns, connect, first_byte, download）
type ResourceTiming struct {
	Start    int64 `json:"start,omitempty"`    // 开始时间（相对于事件时间的纳秒偏移）
	Duration int64 `json:"duration,omitempty"` // 持续时间（纳秒）
}

// ResourceData 表示完整的 Datadog RUM Resource 数据。
type ResourceData struct {
	// 基本字段
	ID         string `json:"id,omitempty"`          // 资源唯一 ID
	Type       string `json:"type,omitempty"`        // 资源类型 (document, xhr, fetch, image, css, js, etc.)
	URL        string `json:"url,omitempty"`         // 资源 URL
	Method     string `json:"method,omitempty"`      // HTTP 方法 (GET, POST, PUT, DELETE, etc.)
	StatusCode int    `json:"status_code,omitempty"` // HTTP 状态码
	Duration   int64  `json:"duration,omitempty"`    // 总耗时（纳秒）

	// 传输信息
	DeliveryType         string `json:"delivery_type,omitempty"`          // 传输类型 (cache, network, etc.)
	RenderBlockingStatus string `json:"render_blocking_status,omitempty"` // 是否阻塞渲染

	// 大小信息（字节）
	Size            int64 `json:"size,omitempty"`              // 解码后的大小
	EncodedBodySize int64 `json:"encoded_body_size,omitempty"` // 编码后的大小
	DecodedBodySize int64 `json:"decoded_body_size,omitempty"` // 解码后的大小
	TransferSize    int64 `json:"transfer_size,omitempty"`     // 传输大小

	// 协议信息
	Protocol string `json:"protocol,omitempty"` // HTTP 协议版本

	// 时间详情
	DNS       *ResourceTiming `json:"dns,omitempty"`        // DNS 查询时间
	Connect   *ResourceTiming `json:"connect,omitempty"`    // TCP 连接时间（含 SSL/TLS）
	FirstByte *ResourceTiming `json:"first_byte,omitempty"` // 首字节到达时间（TTFB）
	Download  *ResourceTiming `json:"download,omitempty"`   // 下载时间
}

// Counter 表示计数器结构
type Counter struct {
	Count int `json:"count,omitempty"` // 计数
}

// ViewPerformanceMetrics 表示视图性能指标详情
type ViewPerformanceMetrics struct {
	// Cumulative Layout Shift 累积布局移位
	CLS *struct {
		Score          float64 `json:"score,omitempty"`           // CLS 分数
		Timestamp      int64   `json:"timestamp,omitempty"`       // 时间戳（纳秒）
		TargetSelector string  `json:"target_selector,omitempty"` // 目标 DOM 选择器
		PreviousRect   *Rect   `json:"previous_rect,omitempty"`   // 移位前的矩形
		CurrentRect    *Rect   `json:"current_rect,omitempty"`    // 移位后的矩形
	} `json:"cls,omitempty"`

	// First Contentful Paint 首次内容绘制
	FCP *struct {
		Timestamp int64 `json:"timestamp,omitempty"` // 时间戳（纳秒）
	} `json:"fcp,omitempty"`

	// Interaction to Next Paint 交互到下一次绘制
	INP *struct {
		Duration       int64  `json:"duration,omitempty"`        // 持续时间（纳秒）
		Timestamp      int64  `json:"timestamp,omitempty"`       // 时间戳（纳秒）
		TargetSelector string `json:"target_selector,omitempty"` // 目标 DOM 选择器
	} `json:"inp,omitempty"`

	// Largest Contentful Paint 最大内容绘制
	LCP *struct {
		Timestamp      int64  `json:"timestamp,omitempty"`       // 时间戳（纳秒）
		TargetSelector string `json:"target_selector,omitempty"` // 目标 DOM 选择器
		ResourceURL    string `json:"resource_url,omitempty"`    // 资源 URL
	} `json:"lcp,omitempty"`
}

// Rect 表示矩形区域信息
type Rect struct {
	X      float64 `json:"x,omitempty"`      // X 坐标
	Y      float64 `json:"y,omitempty"`      // Y 坐标
	Width  float64 `json:"width,omitempty"`  // 宽度
	Height float64 `json:"height,omitempty"` // 高度
}

// DDConfiguration 表示 Datadog SDK 配置信息
type DDConfiguration struct {
	SessionSampleRate       int  `json:"session_sample_rate,omitempty"`        // 会话采样率
	SessionReplaySampleRate int  `json:"session_replay_sample_rate,omitempty"` // 会话回放采样率
	ProfilingSampleRate     int  `json:"profiling_sample_rate,omitempty"`      // 性能分析采样率
	TraceSampleRate         int  `json:"trace_sample_rate,omitempty"`          // 链路追踪采样率
	BetaEncodeCookieOptions bool `json:"beta_encode_cookie_options,omitempty"` // Cookie 编码选项
}

// DDActionTarget 表示用户交互目标信息（按钮、链接等）
type DDActionTarget struct {
	Width    int    `json:"width,omitempty"`    // 目标宽度（像素）
	Height   int    `json:"height,omitempty"`   // 目标高度（像素）
	Selector string `json:"selector,omitempty"` // CSS 选择器
}

// DDActionPosition 表示用户交互位置信息
type DDActionPosition struct {
	X int `json:"x,omitempty"` // X 坐标（相对于视口）
	Y int `json:"y,omitempty"` // Y 坐标（相对于视口）
}

// DDAction 表示 SDK 层面的用户动作信息（用于 click 事件追踪）
type DDAction struct {
	Target     *DDActionTarget   `json:"target,omitempty"`      // 目标元素信息
	Position   *DDActionPosition `json:"position,omitempty"`    // 点击位置
	NameSource string            `json:"name_source,omitempty"` // 动作名称来源（text_content, placeholder 等）
}

// DDData 表示 Datadog 元数据的完整结构
type DDData struct {
	FormatVersion int              `json:"format_version,omitempty"` // 格式版本
	Drift         int              `json:"drift,omitempty"`          // 时间漂移（毫秒）
	Configuration *DDConfiguration `json:"configuration,omitempty"`  // SDK 配置信息
	SDKName       string           `json:"sdk_name,omitempty"`       // SDK 名称（rum, logs 等）
	Discarded     bool             `json:"discarded,omitempty"`      // 是否丢弃该事件
	SpanID        string           `json:"span_id,omitempty"`        // 链路追踪 Span ID
	TraceID       string           `json:"trace_id,omitempty"`       // 链路追踪 Trace ID
	Action        *DDAction        `json:"action,omitempty"`         // SDK 层面追踪的用户动作
}

// ViewData 表示 Datadog RUM 视图信息的强类型结构
type ViewData struct {
	// 基本信息
	URL      string `json:"url,omitempty"`       // 页面 URL
	Referrer string `json:"referrer,omitempty"`  // 引用页面
	ID       string `json:"id,omitempty"`        // 视图唯一 ID
	IsActive bool   `json:"is_active,omitempty"` // 是否活跃

	// 用户交互计数
	Action      *Counter `json:"action,omitempty"`      // 用户操作计数
	Error       *Counter `json:"error,omitempty"`       // 错误计数
	LongTask    *Counter `json:"long_task,omitempty"`   // 长任务计数
	Resource    *Counter `json:"resource,omitempty"`    // 资源计数
	Frustration *Counter `json:"frustration,omitempty"` // 用户挫折信号计数

	// 布局移位信息
	CumulativeLayoutShift       float64 `json:"cumulative_layout_shift,omitempty"`                 // CLS 分数
	CumulativeLayoutShiftTime   int64   `json:"cumulative_layout_shift_time,omitempty"`            // CLS 时间戳（纳秒）
	CumulativeLayoutShiftTarget string  `json:"cumulative_layout_shift_target_selector,omitempty"` // CLS 目标选择器

	// 加载性能指标（纳秒）
	FirstByte              int64 `json:"first_byte,omitempty"`               // TTFB 首字节时间
	DOMInteractive         int64 `json:"dom_interactive,omitempty"`          // DOM 交互时间
	DOMContentLoaded       int64 `json:"dom_content_loaded,omitempty"`       // DOM 内容加载完成时间
	DOMComplete            int64 `json:"dom_complete,omitempty"`             // DOM 完全加载时间
	LoadEvent              int64 `json:"load_event,omitempty"`               // 页面加载事件触发时间
	FirstContentfulPaint   int64 `json:"first_contentful_paint,omitempty"`   // FCP 首次内容绘制时间
	LargestContentfulPaint int64 `json:"largest_contentful_paint,omitempty"` // LCP 最大内容绘制时间

	// 交互性能指标（纳秒）
	InteractionToNextPaint       int64  `json:"interaction_to_next_paint,omitempty"`                 // INP 交互到下一次绘制
	InteractionToNextPaintTime   int64  `json:"interaction_to_next_paint_time,omitempty"`            // INP 时间戳
	InteractionToNextPaintTarget string `json:"interaction_to_next_paint_target_selector,omitempty"` // INP 目标选择器

	// 加载类型和时间
	LoadingType string `json:"loading_type,omitempty"` // 加载类型 (initial_load, route_change, etc.)
	LoadingTime int64  `json:"loading_time,omitempty"` // 页面加载耗时（纳秒）
	TimeSpent   int64  `json:"time_spent,omitempty"`   // 用户在视图中花费的时间（纳秒）

	// 最大内容绘制目标选择器
	LargestContentfulPaintTarget string `json:"largest_contentful_paint_target_selector,omitempty"`

	// 性能指标详情
	Performance *ViewPerformanceMetrics `json:"performance,omitempty"` // 详细性能指标
}

// rumEventRaw 用于解析时兼容 timestamp 备用字段。
type rumEventRaw struct {
	DatadogEventV2
	Timestamp int64 `json:"timestamp,omitempty"`
}

func unmarshalEvent(b []byte) (*DatadogEventV2, error) {
	var raw rumEventRaw
	if err := json.Unmarshal(b, &raw); err != nil {
		return nil, err
	}
	if raw.Date == 0 && raw.Timestamp != 0 {
		raw.Date = raw.Timestamp
	}
	return &raw.DatadogEventV2, nil
}

// parseJSONObjectOrArray 将字节解析为单个对象或数组，不再尝试 NDJSON。
func parseJSONObjectOrArray(buf []byte) ([]*DatadogEventV2, error) {
	var first byte
	for _, b := range buf {
		if b != ' ' && b != '\t' && b != '\n' && b != '\r' {
			first = b
			break
		}
	}

	if first == '[' {
		var raws []json.RawMessage
		if err := json.Unmarshal(buf, &raws); err != nil {
			return nil, fmt.Errorf("invalid json format: %w", err)
		}
		logger.Debugf("parse success: format=array, items=%d", len(raws))
		events := make([]*DatadogEventV2, 0, len(raws))
		for _, r := range raws {
			ev, err := unmarshalEvent(r)
			if err != nil {
				logger.Debugf("skip array item: %v", err)
				continue
			}
			events = append(events, ev)
		}
		return events, nil
	}

	ev, err := unmarshalEvent(buf)
	if err != nil {
		return nil, fmt.Errorf("invalid json format: %w", err)
	}
	logger.Debugf("parse success: format=single object")
	return []*DatadogEventV2{ev}, nil
}

// 支持 NDJSON / 单对象 / 数组 自动识别
func parseDatadogRUM(buf []byte) ([]*DatadogEventV2, error) {
	// panic 保护
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("parseDatadogRUM panic: %v\nstack: %s", r, debug.Stack())
		}
	}()

	logger.Debugf("parseDatadogRUM: data length=%d, preview: %s", len(buf), string(buf[:min(len(buf), 300)]))

	records, ndjsonSuccess, stats := tryParseNDJSON(buf)
	if ndjsonSuccess {
		logger.Debugf("parse success: format=NDJSON, total=%d, success=%d, failed=%d, skipped=%d",
			stats.totalLines, stats.successLines, stats.failedLines, stats.skippedLines)
		return records, nil
	}

	return parseJSONObjectOrArray(buf)
}

// parseStats 解析统计信息
type parseStats struct {
	totalLines   int
	successLines int
	failedLines  int
	skippedLines int
}

// tryParseNDJSON 流式解析 NDJSON，直接反序列化到 *DatadogEventV2
func tryParseNDJSON(buf []byte) ([]*DatadogEventV2, bool, parseStats) {
	var records []*DatadogEventV2
	var stats parseStats

	reader := bufio.NewReader(bytes.NewReader(buf))

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && err != io.EOF {
			logger.Debugf("ndjson read error: %v", err)
			return nil, false, stats
		}

		if err == io.EOF && len(line) == 0 {
			break
		}

		stats.totalLines++
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			stats.skippedLines++
		} else {
			ev, unmarshalErr := unmarshalEvent(line)
			if unmarshalErr != nil {
				stats.failedLines++
				logger.Debugf("line %d parse failed: %v", stats.totalLines, unmarshalErr)
			} else {
				records = append(records, ev)
				stats.successLines++
			}
		}

		if err == io.EOF {
			break
		}
	}

	// 解析到有效记录才算成功
	return records, len(records) > 0, stats
}

// parseDatadogRUMV2 解析 Datadog Browser SDK RUM 数据
func parseDatadogRUMV2(buf []byte) ([]*DatadogEventV2, error) {
	defer func() {
		if r := recover(); r != nil {
			logger.Errorf("parseDatadogRUMV2 panic: %v\nstack: %s", r, debug.Stack())
		}
	}()

	logger.Debugf("parseDatadogRUMV2: data length=%d", len(buf))

	// 首先尝试流式解析 JSON Lines 格式
	records, success, stats := tryParseNDJSON(buf)
	if success {
		logger.Debugf("parse v2 success: format=NDJSON, total=%d, success=%d, failed=%d, skipped=%d",
			stats.totalLines, stats.successLines, stats.failedLines, stats.skippedLines)
		return records, nil
	}

	// NDJSON 失败，直接解析单对象或数组，避免再次扫描
	return parseJSONObjectOrArray(buf)
}
