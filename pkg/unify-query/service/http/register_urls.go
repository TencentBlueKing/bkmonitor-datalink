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

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/infos"
)

type RegisterHandlers struct {
	ctx context.Context
	g   *gin.RouterGroup
}

func (r *RegisterHandlers) register(method, handlerPath string, handlerFunc ...gin.HandlerFunc) {
	switch method {
	case http.MethodGet:
		r.g.GET(handlerPath, handlerFunc...)
	case http.MethodPost:
		r.g.POST(handlerPath, handlerFunc...)
	case http.MethodHead:
		r.g.HEAD(handlerPath, handlerFunc...)
	default:
		log.Errorf(r.ctx, "registerHandlers error type is error %s", method)
		return
	}

	log.Infof(r.ctx, "registerHandlers => [%s] %s", method, handlerPath)

	metadata.AddHandler(handlerPath, handlerFunc...)
}

func getRegisterHandlers(ctx context.Context, g *gin.RouterGroup) *RegisterHandlers {
	return &RegisterHandlers{
		ctx: ctx,
		g:   g,
	}
}

func registerDefaultHandlers(ctx context.Context, g *gin.RouterGroup) {
	var handlerPath string

	registerHandler := getRegisterHandlers(ctx, g)

	// query/ts
	handlerPath = viper.GetString(TSQueryHandlePathConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, HandlerQueryTs)

	// query/ts/promql
	handlerPath = viper.GetString(TSQueryPromQLHandlePathConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, HandlerQueryPromQL)

	// query/reference
	handlerPath = viper.GetString(TSQueryReferenceQueryHandlePathConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, HandlerQueryReference)

	// query/raw
	handlerPath = viper.GetString(TSQueryRawQueryHandlePathConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, HandlerQueryRaw)

	// query/ts/exemplar
	handlerPath = viper.GetString(TSQueryExemplarHandlePathConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, HandlerQueryExemplar)

	// query/ts/info
	infoPath := viper.GetString(TSQueryInfoHandlePathConfigPath)

	// query/ts/info/field_keys
	handlerPath = path.Join(infoPath, string(infos.FieldKeys))
	registerHandler.register(http.MethodPost, handlerPath, HandlerFieldKeys)

	// query/ts/info/tag_keys
	handlerPath = path.Join(infoPath, string(infos.TagKeys))
	registerHandler.register(http.MethodPost, handlerPath, HandlerTagKeys)

	// query/ts/info/tag_values
	handlerPath = path.Join(infoPath, string(infos.TagValues))
	registerHandler.register(http.MethodPost, handlerPath, HandlerTagValues)

	// query/ts/info/series
	handlerPath = path.Join(infoPath, string(infos.Series))
	registerHandler.register(http.MethodPost, handlerPath, HandlerSeries)

	// query/ts/info/time_series
	handlerPath = path.Join(infoPath, string(infos.TimeSeries))
	registerHandler.register(http.MethodPost, handlerPath, HandleTimeSeries)

	// query/ts/label/:label_name/values
	handlerPath = viper.GetString(TSQueryLabelValuesPathConfigPath)
	registerHandler.register(http.MethodGet, handlerPath, HandlerLabelValues)

	// query/ts/cluster_metrics/
	handlerPath = viper.GetString(TSQueryClusterMetricsPathConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, HandlerQueryTsClusterMetrics)

	// query/es/
	handlerPath = viper.GetString(ESHandlePathConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, HandleESQueryRequest)

	// query/apigw
	handlerPath = viper.GetString(ApiGwConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, HandleAPIGW)
}

func registerOtherHandlers(ctx context.Context, g *gin.RouterGroup) {
	var handlerPath string

	registerHandler := getRegisterHandlers(ctx, g)

	// register prometheus metrics
	if viper.GetBool(EnablePrometheusConfigPath) {
		handlerPath = viper.GetString(PrometheusPathConfigPath)
		registerHandler.register(http.MethodGet, handlerPath, gin.WrapH(
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
	registerHandler.register(http.MethodPost, handlerPath, HandlerStructToPromQL)

	// query/ts/promql_to_struct
	handlerPath = viper.GetString(TSQueryPromQLToStructHandlePathConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, HandlerPromQLToStruct)

	// check/query/ts
	handlerPath = viper.GetString(CheckQueryTsConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, HandlerCheckQueryTs)

	// check/query/ts/promql
	handlerPath = viper.GetString(CheckQueryPromQLConfigPath)
	registerHandler.register(http.MethodPost, handlerPath, HandlerCheckQueryPromQL)

	// print
	handlerPath = viper.GetString(PrintHandlePathConfigPath)
	registerHandler.register(http.MethodGet, handlerPath, HandlePrint)

	// influxdb_print
	handlerPath = viper.GetString(InfluxDBPrintHandlePathConfigPath)
	registerHandler.register(http.MethodGet, handlerPath, HandleInfluxDBPrint)

	// ff
	handlerPath = viper.GetString(FeatureFlagHandlePathConfigPath)
	registerHandler.register(http.MethodGet, handlerPath, HandleFeatureFlag)

	// space_print
	handlerPath = viper.GetString(SpacePrintHandlePathConfigPath)
	registerHandler.register(http.MethodGet, handlerPath, HandleSpacePrint)

	// space_key_print
	handlerPath = viper.GetString(SpaceKeyPrintHandlePathConfigPath)
	registerHandler.register(http.MethodGet, handlerPath, HandleSpaceKeyPrint)

	// tsdb_print
	handlerPath = viper.GetString(TsDBPrintHandlePathConfigPath)
	registerHandler.register(http.MethodGet, handlerPath, HandleTsDBPrint)

	// HEAD
	registerHandler.register(http.MethodHead, "", HandlerHealth)

	// profile
	if viper.GetBool(EnableProfileConfigPath) {
		registerProfile(ctx, g)
	}
}
