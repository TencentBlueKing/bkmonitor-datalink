// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package prometheus

import (
	"github.com/pkg/errors"
)

// Prometheus 查询相关的错误定义
var (
	// ErrTimeRangeTooLarge 时间范围过大的错误
	// 当查询的时间范围超过允许的最大值时返回此错误
	ErrTimeRangeTooLarge = errors.New("time range is too large")

	// ErrDatetimeParseFailed 日期时间解析失败的错误
	// 当无法解析日期时间字符串时返回此错误
	ErrDatetimeParseFailed = errors.New("datetime parser failed")

	// ErrOperatorType 未知操作符类型的错误
	// 当遇到不支持的操作符类型时返回此错误
	ErrOperatorType = errors.New("unknown operator type")

	// ErrPromQueryInfoNotSet Prometheus 查询信息未设置的错误
	// 当查询信息未正确初始化时返回此错误
	ErrPromQueryInfoNotSet = errors.New("prom query info not set")

	// ErrGetQueryByMetricFailed 根据指标获取查询信息失败的错误
	// 当无法根据指标名称获取对应的查询信息时返回此错误
	ErrGetQueryByMetricFailed = errors.New("cannot get query info of metric")

	// ErrGetMetricMappingFailed 获取指标映射失败的错误
	// 当无法获取指标的映射关系时返回此错误
	ErrGetMetricMappingFailed = errors.New("get metric mapping failed")

	// ErrContextDone 上下文已完成的错误
	// 当上下文被取消或超时时返回此错误
	ErrContextDone = errors.New("context done")

	// ErrTimeout 超时错误
	// 当操作执行超时时返回此错误
	ErrTimeout = errors.New("time out")

	// ErrInvalidValue 无效值的错误
	// 当遇到无效的数值或参数时返回此错误
	ErrInvalidValue = errors.New("invalid value")

	// ErrChannelReceived 通道关闭错误
	// 当通道在接收到值之前被关闭时返回此错误
	ErrChannelReceived = errors.New("channel closed before a value received")
)
