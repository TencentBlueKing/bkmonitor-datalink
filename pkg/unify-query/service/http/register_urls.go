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
	"context"
	"net/http"
	"path"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/viper"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/infos"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
)

type RegisterHandlers struct {
	ctx     context.Context
	g       *gin.RouterGroup
	entries []HandlerEntry
}

func (r *RegisterHandlers) register(method, handlerPath string, options ...RegisterOptionFunc) {
	var op RegisterOption

	for _, option := range options {
		op = option(&op)
	}

	r.entries = append(r.entries, HandlerEntry{
		Method:          method,
		HandlerPath:     handlerPath,
		HandlerFunc:     op.Handler,
		IsProxyEndpoint: op.IsProxyEndpoint,
	})
}

type RegisterOption struct {
	IsProxyEndpoint bool
	Handler         []gin.HandlerFunc
}

type RegisterOptionFunc func(*RegisterOption) RegisterOption

func WithProxyEndpoint() RegisterOptionFunc {
	return func(op *RegisterOption) RegisterOption {
		op.IsProxyEndpoint = true
		return *op
	}
}

func WithHandler(handler ...gin.HandlerFunc) RegisterOptionFunc {
	return func(op *RegisterOption) RegisterOption {
		op.Handler = append(op.Handler, handler...)
		return *op
	}
}

func (r *RegisterHandlers) do() {
	for _, entry := range r.entries {
		if len(entry.HandlerFunc) == 0 {
			continue
		}

		if !entry.IsProxyEndpoint {
			metadata.AddHandler(entry.HandlerPath, entry.HandlerFunc...)
		}

		switch entry.Method {
		case http.MethodGet:
			r.g.GET(entry.HandlerPath, entry.HandlerFunc...)
		case http.MethodPost:
			r.g.POST(entry.HandlerPath, entry.HandlerFunc...)
		case http.MethodHead:
			r.g.HEAD(entry.HandlerPath, entry.HandlerFunc...)
		default:
			r.g.Handle(entry.Method, entry.HandlerPath, entry.HandlerFunc...)
		}
	}
}

func getRegisterHandlers(ctx context.Context, g *gin.RouterGroup) *RegisterHandlers {
	return &RegisterHandlers{
		ctx: ctx,
		g:   g,
	}
}

type HandlerEntry struct {
	Method          string
	HandlerPath     string
	HandlerFunc     []gin.HandlerFunc
	IsProxyEndpoint bool
}

func registerDefaultHandlers(ctx context.Context, g *gin.RouterGroup) {
	var handlerPath string
	registerHandler := getRegisterHandlers(ctx, g)

	// query/ts
	handlerPath = viper.GetString(TSQueryHandlePathConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, WithHandler(HandlerQueryTs))

	// query/ts/promql
	handlerPath = viper.GetString(TSQueryPromQLHandlePathConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, WithHandler(HandlerQueryPromQL))

	// query/reference
	handlerPath = viper.GetString(TSQueryReferenceQueryHandlePathConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, WithHandler(HandlerQueryReference))

	// query/raw
	handlerPath = viper.GetString(TSQueryRawQueryHandlePathConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, WithHandler(HandlerQueryRaw))

	// query/ts/exemplar
	handlerPath = viper.GetString(TSQueryExemplarHandlePathConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, WithHandler(HandlerQueryExemplar))

	// query/ts/info
	infoPath := viper.GetString(TSQueryInfoHandlePathConfigPath)

	// query/ts/info/field_keys
	handlerPath = path.Join(infoPath, string(infos.FieldKeys))
	registerHandler.register(http.MethodPost, handlerPath, WithHandler(HandlerFieldKeys))

	// query/ts/info/tag_keys
	handlerPath = path.Join(infoPath, string(infos.TagKeys))
	registerHandler.register(http.MethodPost, handlerPath, WithHandler(HandlerTagKeys))

	// query/ts/info/tag_values
	handlerPath = path.Join(infoPath, string(infos.TagValues))
	registerHandler.register(http.MethodPost, handlerPath, WithHandler(HandlerTagValues))

	// query/ts/info/series
	handlerPath = path.Join(infoPath, string(infos.Series))
	registerHandler.register(http.MethodPost, handlerPath, WithHandler(HandlerSeries))

	// query/ts/info/time_series
	handlerPath = path.Join(infoPath, string(infos.TimeSeries))
	registerHandler.register(http.MethodPost, handlerPath, WithHandler(HandleTimeSeries))

	// query/ts/label/:label_name/values
	handlerPath = viper.GetString(TSQueryLabelValuesPathConfigPath)
	registerHandler.register(http.MethodGet, handlerPath, WithHandler(HandlerLabelValues))

	// query/ts/cluster_metrics/
	handlerPath = viper.GetString(TSQueryClusterMetricsPathConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, WithHandler(HandlerQueryTsClusterMetrics))

	// query/es/
	handlerPath = viper.GetString(ESHandlePathConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, WithHandler(HandleESQueryRequest))

	// query/proxy
	handlerPath = viper.GetString(ProxyConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, WithHandler(proxy.HandleAPIGW), WithProxyEndpoint())

	registerHandler.do()

}

func registerOtherHandlers(ctx context.Context, g *gin.RouterGroup) {
	var handlerPath string

	registerHandler := getRegisterHandlers(ctx, g)

	// register prometheus metrics
	if viper.GetBool(EnablePrometheusConfigPath) {
		handlerPath = viper.GetString(PrometheusPathConfigPath)
		registerHandler.register(http.MethodGet, handlerPath, WithHandler(gin.WrapH(
			promhttp.HandlerFor(
				prometheus.DefaultGatherer,
				promhttp.HandlerOpts{
					EnableOpenMetrics: true,
				},
			),
		)))
	}

	// query/ts/struct_to_promql
	handlerPath = viper.GetString(TSQueryStructToPromQLHandlePathConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, WithHandler(HandlerStructToPromQL))

	// query/ts/promql_to_struct
	handlerPath = viper.GetString(TSQueryPromQLToStructHandlePathConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, WithHandler(HandlerPromQLToStruct))

	// check/query/ts
	handlerPath = viper.GetString(CheckQueryTsConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, WithHandler(HandlerCheckQueryTs))

	// check/query/ts/promql
	handlerPath = viper.GetString(CheckQueryPromQLConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, WithHandler(HandlerCheckQueryPromQL))

	// print
	handlerPath = viper.GetString(PrintHandlePathConfigPath)
	registerHandler.register(http.MethodGet, handlerPath, WithHandler(HandlePrint))

	// influxdb_print
	handlerPath = viper.GetString(InfluxDBPrintHandlePathConfigPath)
	registerHandler.register(http.MethodGet, handlerPath, WithHandler(HandleInfluxDBPrint))

	// ff
	handlerPath = viper.GetString(FeatureFlagHandlePathConfigPath)
	registerHandler.register(http.MethodGet, handlerPath, WithHandler(HandleFeatureFlag))

	// space_print
	handlerPath = viper.GetString(SpacePrintHandlePathConfigPath)
	registerHandler.register(http.MethodGet, handlerPath, WithHandler(HandleSpacePrint))

	// space_key_print
	handlerPath = viper.GetString(SpaceKeyPrintHandlePathConfigPath)
	registerHandler.register(http.MethodGet, handlerPath, WithHandler(HandleSpaceKeyPrint))

	// tsdb_print
	handlerPath = viper.GetString(TsDBPrintHandlePathConfigPath)
	registerHandler.register(http.MethodGet, handlerPath, WithHandler(HandleTsDBPrint))

	// HEAD
	registerHandler.register(http.MethodHead, "", WithHandler(HandlerHealth))

	// profile
	if viper.GetBool(EnableProfileConfigPath) {
		registerProfile(ctx, g)
	}

	registerHandler.do()
}
