// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"fmt"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

// 消息类型常量定义，用于标识不同的操作类型和查询类型
const (
	// 解析器相关消息类型
	MsgParserUnifyQuery = "unify_query_parser" // 统一查询解析器
	MsgParserSQL        = "sql_parser"         // SQL 解析器
	MsgParserDoris      = "doris_parser"       // Doris 解析器
	MsgParserLucene     = "lucene_parser"      // Lucene 解析器
	MsgParserPromQL     = "promql_parser"      // PromQL 解析器

	// 查询相关消息类型
	MsgQueryRedis           = "redis_query"            // Redis 查询
	MsgQueryES              = "es_query"               // Elasticsearch 查询
	MsgQueryVictoriaMetrics = "victoria_metrics_query" // VictoriaMetrics 查询
	MsgQueryBKSQL           = "bk_sql_query"           // BKSQL 查询
	MsgQueryInfluxDB        = "influxdb_query"         // InfluxDB 查询

	// 转换相关消息类型
	MsgTransformTs     = "transform_ts"     // 时序数据转换
	MsgTransformPromQL = "transform_promql" // PromQL 转换

	// 具体查询操作消息类型
	MsgQueryPromQL         = "query_promql"          // PromQL 查询
	MsgQueryRelation       = "query_relation"        // 关系查询
	MsgQueryInfo           = "query_info"            // 信息查询
	MsgQueryTs             = "query_ts"              // 时序查询
	MsgQueryReference      = "query_reference"       // 引用查询
	MsgQueryRaw            = "query_raw"             // 原始查询
	MsgQueryRawScroll      = "query_raw_scroll"      // 原始滚动查询
	MsgQueryExemplar       = "query_exemplar"        // 示例查询
	MsgQueryClusterMetrics = "query_cluster_metrics" // 集群指标查询

	// 其他操作消息类型
	MsgRedisLock = "redis_lock" // Redis 锁

	// 处理器相关消息类型
	MsgHandlerAPI  = "handler_api"  // API 处理器
	MsgTableFormat = "table_format" // 表格格式化

	// 路由和功能标志
	MsgQueryRouter = "query_router" // 查询路由
	MsgFeatureFlag = "feature_flag" // 功能标志

	// HTTP 请求
	MsgHttpCurl = "http_curl" // HTTP 请求
)

// Message 表示一个消息对象，用于记录操作信息和错误信息
type Message struct {
	ID      string // 消息标识符，用于标识消息类型
	Content string // 消息内容，格式化后的字符串
}

// NewMessage 创建一个新的消息对象
// 参数:
//   - id: 消息标识符
//   - format: 格式化字符串，支持 fmt.Sprintf 的格式化语法
//   - args: 格式化参数
//
// 返回: 新创建的消息对象指针
func NewMessage(id, format string, args ...any) *Message {
	return &Message{
		ID:      id,
		Content: fmt.Sprintf(format, args...),
	}
}

// Text 返回格式化的消息文本，包含消息 ID 和内容
// 格式: [ID] Content
// 返回: 格式化后的消息文本
func (m *Message) Text() string {
	s := fmt.Sprintf("[%s] %s", m.ID, m.Content)
	return s
}

// String 返回消息的内容部分（不包含 ID）
// 实现 fmt.Stringer 接口
// 返回: 消息内容字符串
func (m *Message) String() string {
	return m.Content
}

// Error 将消息转换为错误对象，并记录错误日志
// 如果提供了原始错误 err，会将消息错误包装在原始错误外层
// 参数:
//   - ctx: 上下文对象，用于日志记录
//   - err: 可选的原始错误，如果提供则会被包装
//
// 返回: 错误对象
func (m *Message) Error(ctx context.Context, err error) error {
	s := m.String()
	if s == "" {
		return errors.New("")
	}

	newErr := errors.New(s)
	if err != nil {
		newErr = errors.WithMessage(err, newErr.Error())
	}
	log.Errorf(ctx, "%s", newErr.Error())
	return newErr
}

// Warn 记录警告日志
// 参数:
//   - ctx: 上下文对象，用于日志记录
func (m *Message) Warn(ctx context.Context) {
	log.Warnf(ctx, "%s", m.Text())
}

// Info 记录信息日志
// 参数:
//   - ctx: 上下文对象，用于日志记录
func (m *Message) Info(ctx context.Context) {
	log.Infof(ctx, "%s", m.Text())
}

// Status 设置状态码并记录警告日志
// 参数:
//   - ctx: 上下文对象，用于状态设置和日志记录
//   - code: 状态码字符串
func (m *Message) Status(ctx context.Context, code string) {
	SetStatus(ctx, code, m.String())
	log.Warnf(ctx, "%s", m.Text())
}
