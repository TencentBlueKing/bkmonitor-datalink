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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/infos"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/endpoint"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
)

func registerDefaultHandlers(ctx context.Context, g *gin.RouterGroup) {
	var handlerPath string
	registerHandler := endpoint.NewRegisterHandler(ctx, g)

	// query/ts
	handlerPath = viper.GetString(TSQueryHandlePathConfigPath)
	registerHandler.Register(http.MethodPost, handlerPath, HandlerQueryTs)

	// query/ts/promql
	handlerPath = viper.GetString(TSQueryPromQLHandlePathConfigPath)
	registerHandler.Register(http.MethodPost, handlerPath, HandlerQueryPromQL)

	// query/reference
	handlerPath = viper.GetString(TSQueryReferenceQueryHandlePathConfigPath)
	registerHandler.Register(http.MethodPost, handlerPath, HandlerQueryReference)

	// query/raw
	handlerPath = viper.GetString(TSQueryRawQueryHandlePathConfigPath)
	registerHandler.Register(http.MethodPost, handlerPath, HandlerQueryRaw)

	// query/raw/with_scroll
	handlerPath = viper.GetString(TSQueryRawQueryWithScrollHandlePathConfigPath)
	registerHandler.Register(http.MethodPost, handlerPath, HandlerQueryRawWithScroll)

	// query/ts/exemplar
	handlerPath = viper.GetString(TSQueryExemplarHandlePathConfigPath)
	registerHandler.Register(http.MethodPost, handlerPath, HandlerQueryExemplar)

	// query/ts/info
	infoPath := viper.GetString(TSQueryInfoHandlePathConfigPath)

	// query/ts/info/field_keys
	handlerPath = path.Join(infoPath, string(infos.FieldKeys))
	registerHandler.Register(http.MethodPost, handlerPath, HandlerFieldKeys)

	// query/ts/info/tag_keys
	handlerPath = path.Join(infoPath, string(infos.TagKeys))
	registerHandler.Register(http.MethodPost, handlerPath, HandlerTagKeys)

	// query/ts/info/tag_values
	handlerPath = path.Join(infoPath, string(infos.TagValues))
	registerHandler.Register(http.MethodPost, handlerPath, HandlerTagValues)

	// query/ts/info/series
	handlerPath = path.Join(infoPath, string(infos.Series))
	registerHandler.Register(http.MethodPost, handlerPath, HandlerSeries)

	// query/ts/info/time_series
	handlerPath = path.Join(infoPath, string(infos.TimeSeries))
	registerHandler.Register(http.MethodPost, handlerPath, HandleTimeSeries)

	// query/ts/label/:label_name/values
	handlerPath = viper.GetString(TSQueryLabelValuesPathConfigPath)
	registerHandler.Register(http.MethodGet, handlerPath, HandlerLabelValues)

	// query/ts/info/field_map
	handlerPath = path.Join(infoPath, string(infos.FieldMap))
	registerHandler.Register(http.MethodPost, handlerPath, HandlerFieldMap)

	// query/ts/cluster_metrics/
	handlerPath = viper.GetString(TSQueryClusterMetricsPathConfigPath)
	registerHandler.Register(http.MethodPost, handlerPath, HandlerQueryTsClusterMetrics)

	// query/es/
	handlerPath = viper.GetString(ESHandlePathConfigPath)
	registerHandler.Register(http.MethodPost, handlerPath, HandleESQueryRequest)
}

func registerOtherHandlers(ctx context.Context, g *gin.RouterGroup) {
	var handlerPath string

	registerHandler := endpoint.NewRegisterHandler(ctx, g)

	// register prometheus metrics
	if viper.GetBool(EnablePrometheusConfigPath) {
		handlerPath = viper.GetString(PrometheusPathConfigPath)
		registerHandler.Register(http.MethodGet, handlerPath, gin.WrapH(
			promhttp.HandlerFor(
				prometheus.DefaultGatherer,
				promhttp.HandlerOpts{
					EnableOpenMetrics: true,
				},
			),
		))
	}

	// query/ts/struct_to_promql
	handlerPath = viper.GetString(TSQueryStructToPromQLHandlePathConfigPath)
	registerHandler.Register(http.MethodPost, handlerPath, HandlerStructToPromQL)

	// query/ts/promql_to_struct
	handlerPath = viper.GetString(TSQueryPromQLToStructHandlePathConfigPath)
	registerHandler.Register(http.MethodPost, handlerPath, HandlerPromQLToStruct)

	// check/query/ts
	handlerPath = viper.GetString(CheckQueryTsConfigPath)
	registerHandler.Register(http.MethodPost, handlerPath, HandlerCheckQueryTs)

	// check/query/ts/promql
	handlerPath = viper.GetString(CheckQueryPromQLConfigPath)
	registerHandler.Register(http.MethodPost, handlerPath, HandlerCheckQueryPromQL)

	// print
	handlerPath = viper.GetString(PrintHandlePathConfigPath)
	registerHandler.Register(http.MethodGet, handlerPath, HandlePrint)

	// influxdb_print
	handlerPath = viper.GetString(InfluxDBPrintHandlePathConfigPath)
	registerHandler.Register(http.MethodGet, handlerPath, HandleInfluxDBPrint)

	// ff
	handlerPath = viper.GetString(FeatureFlagHandlePathConfigPath)
	registerHandler.Register(http.MethodGet, handlerPath, HandleFeatureFlag)

	// space_print
	handlerPath = viper.GetString(SpacePrintHandlePathConfigPath)
	registerHandler.Register(http.MethodGet, handlerPath, HandleSpacePrint)

	// space_key_print
	handlerPath = viper.GetString(SpaceKeyPrintHandlePathConfigPath)
	registerHandler.Register(http.MethodGet, handlerPath, HandleSpaceKeyPrint)

	// tsdb_print
	handlerPath = viper.GetString(TsDBPrintHandlePathConfigPath)
	registerHandler.Register(http.MethodGet, handlerPath, HandleTsDBPrint)

	// HEAD
	registerHandler.Register(http.MethodHead, "", HandlerHealth)

	// profile
	if viper.GetBool(EnableProfileConfigPath) {
		registerProfile(ctx, g)
	}
}

func registerProxyHandler(ctx context.Context, g *gin.RouterGroup) {
	var handlerPath string
	registerHandler := endpoint.NewRegisterHandler(ctx, g)

	handlerPath = viper.GetString(ProxyConfigPath)
	registerHandler.RegisterWithOutHandlerMap(http.MethodPost, handlerPath, proxy.HandleProxy)
}
