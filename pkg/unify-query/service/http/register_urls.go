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
	"path"

	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/infos"
)

// registerSwagger
func registerSwagger(g *gin.Engine) {
	g.GET("/sagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
}

// registerTSQueryService: /query/ts
func registerTSQueryService(g *gin.Engine) {
	servicePath := viper.GetString(TSQueryHandlePathConfigPath)
	//g.POST(servicePath, HandleTSQueryRequest)
	g.POST(servicePath, HandlerQueryTs)
	log.Infof(context.TODO(), "ts service register in path->[%s]", servicePath)
}

// registerTSQueryPromQLService: /query/ts/promql
func registerTSQueryPromQLService(g *gin.Engine) {
	servicePath := viper.GetString(TSQueryPromQLHandlePathConfigPath)
	//g.POST(servicePath, HandleTsQueryPromQLDataRequest)
	g.POST(servicePath, HandlerQueryPromQL)
	log.Infof(context.TODO(), "ts service register in path->[%s]", servicePath)
}

// registerTSQueryExemplarService: /query/ts/exemplar
func registerTSQueryExemplarService(g *gin.Engine) {
	servicePath := viper.GetString(TSQueryExemplarHandlePathConfigPath)
	//g.POST(servicePath, HandleTSExemplarRequest)
	g.POST(servicePath, HandlerQueryExemplar)
	log.Infof(context.TODO(), "ts service register in path->[%s]", servicePath)
}

// registerTSQueryStructToPromQLService: /query/ts/struct_to_promql
func registerTSQueryStructToPromQLService(g *gin.Engine) {
	servicePath := viper.GetString(TSQueryStructToPromQLHandlePathConfigPath)
	g.POST(servicePath, HandlerStructToPromQL)
	//g.POST(servicePath, HandleTsQueryStructToPromQLRequest)
	log.Infof(context.TODO(), "ts service register in path->[%s]", servicePath)
}

// registerTSQueryPromQLToStructService: /query/ts/promql_to_struct
func registerTSQueryPromQLToStructService(g *gin.Engine) {
	servicePath := viper.GetString(TSQueryPromQLToStructHandlePathConfigPath)
	g.POST(servicePath, HandlerPromQLToStruct)
	//g.POST(servicePath, HandleTsQueryPromQLToStructRequest)
	log.Infof(context.TODO(), "ts service register in path->[%s]", servicePath)
}

// registerTSQueryInfoService: /query/ts/info
func registerTSQueryInfoService(g *gin.Engine) {
	servicePath := viper.GetString(TSQueryInfoHandlePathConfigPath)
	tagKeyPath := path.Join(servicePath, string(infos.TagKeys))
	tagValuesPath := path.Join(servicePath, string(infos.TagValues))
	fieldKeyPath := path.Join(servicePath, string(infos.FieldKeys))
	seriesPath := path.Join(servicePath, string(infos.Series))
	timeSeriesPath := path.Join(servicePath, string(infos.TimeSeries))

	g.POST(fieldKeyPath, HandlerFieldKeys)
	g.POST(tagKeyPath, HandlerTagKeys)
	g.POST(tagValuesPath, HandlerTagValues)
	g.POST(seriesPath, HandlerSeries)

	//g.POST(tagKeyPath, HandleShowTagKeys)
	//g.POST(tagValuesPath, HandleShowTagValues)
	//g.POST(fieldKeyPath, HandleShowFieldKeys)
	//g.POST(seriesPath, HandleShowSeries)
	g.POST(timeSeriesPath, HandleTimeSeries)

	log.Infof(context.TODO(), "ts service register in path->[%s][%s][%s]", tagKeyPath, tagValuesPath, fieldKeyPath)
}

// registerLabelValuesService: /query/ts/label/:label_name/values
func registerLabelValuesService(g *gin.Engine) {
	servicePath := viper.GetString(TSQueryLabelValuesPathConfigPath)

	g.GET(servicePath, HandlerLabelValues)
	//g.GET(servicePath, HandleLabelValuesRequest)

	log.Infof(context.TODO(), "ts service register in path->[%s]", servicePath)
}

// registerPrint: /print
func registerPrint(g *gin.Engine) {
	servicePath := viper.GetString(PrintHandlePathConfigPath)
	g.GET(servicePath, HandlePrint)
	log.Infof(context.TODO(), "ts service register in path->[%s]", servicePath)
}

// registerInfluxDBPrint: /influxdb_print
func registerInfluxDBPrint(g *gin.Engine) {
	servicePath := viper.GetString(InfluxDBPrintHandlePathConfigPath)
	g.GET(servicePath, HandleInfluxDBPrint)
	log.Infof(context.TODO(), "ts service register in path->[%s]", servicePath)
}

// registerFeatureFlag: /ff
func registerFeatureFlag(g *gin.Engine) {
	servicePath := viper.GetString(FeatureFlagHandlePathConfigPath)
	g.GET(servicePath, HandleFeatureFlag)
	log.Infof(context.TODO(), "ts service register in path->[%s]", servicePath)
}

// registerSpacePrint: /space_print
func registerSpacePrint(g *gin.Engine) {
	servicePath := viper.GetString(SpacePrintHandlePathConfigPath)
	g.GET(servicePath, HandleSpacePrint)
	log.Infof(context.TODO(), "ts service register in path->[%s]", servicePath)
}

// registerSpaceKetPrint: /space_print
func registerSpaceKeyPrint(g *gin.Engine) {
	servicePath := viper.GetString(SpaceKeyPrintHandlePathConfigPath)
	g.GET(servicePath, HandleSpaceKeyPrint)
	log.Infof(context.TODO(), "ts service register in path->[%s]", servicePath)
}
