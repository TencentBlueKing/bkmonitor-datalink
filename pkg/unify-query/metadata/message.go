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
	"strings"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

const (
	MsgParserUnifyQuery = "unify_query_parser"
	MsgParserSQL        = "sql_parser"
	MsgParserDoris      = "doris_parser"
	MsgParserLucene     = "lucene_parser"
	MsgParserPromQL     = "promql_parser"

	MsgQueryRedis           = "redis_query"
	MsgQueryES              = "es_query"
	MsgQueryVictoriaMetrics = "victoria_metrics_query"
	MsgQueryBKSQL           = "bk_sql_query"
	MsgQueryInfluxDB        = "influxdb_query"
	MsgQueryPromQL          = "promql_query"
	MsgQueryRelation        = "relation_query"

	MsgQueryInfo = "query_info"
	MsgQueryTs   = "query_ts"

	MsgHandlerAPI = "handler_api"

	MsgTableFormat = "table_format"

	MsgQueryRouter = "query_router"
	MsgFeatureFlag = "feature_flag"
)

type Message struct {
	ID      string
	Content string
}

func Sprintf(id, format string, args ...any) *Message {
	return &Message{
		ID:      id,
		Content: fmt.Sprintf(format, args...),
	}
}

func (m *Message) String() string {
	var s strings.Builder
	s.WriteString(m.ID)
	s.WriteString(": ")
	s.WriteString(m.Content)
	return s.String()
}

func (m *Message) Error(ctx context.Context, err error) error {
	s := m.String()
	if s == "" {
		return nil
	}

	newErr := errors.New(m.String())
	if err != nil {
		newErr = errors.Wrap(newErr, err.Error())
	}
	log.Errorf(ctx, newErr.Error())
	return newErr
}

func (m *Message) Warn(ctx context.Context) {
	log.Warnf(ctx, m.String())
}

func (m *Message) Info(ctx context.Context) {
	log.Infof(ctx, m.String())
}

func (m *Message) Status(ctx context.Context, code string) {
	log.Warnf(ctx, m.String())
	SetStatus(ctx, code, m.String())
}
