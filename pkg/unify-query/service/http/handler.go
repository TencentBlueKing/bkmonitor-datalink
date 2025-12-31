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
	"fmt"
	"unsafe"

	"github.com/gin-gonic/gin"

	influxdbRouter "github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/influxdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/redis"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// HandlerPromQLToStruct
// @Summary  promql to struct
// @ID       transform_promql_to_struct
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                          false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                          false  "空间UID" default(bkcc__2)
// @Param    data                  	body      structured.QueryPromQL  		  true   "json data"
// @Success  200                   	{object}  structured.QueryTs
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/ts/promql_to_struct [post]
func HandlerPromQLToStruct(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		resp = &response{
			c: c,
		}

		err error
	)

	// 这里开始context就使用trace生成的了
	ctx, span := trace.NewSpan(ctx, "handle-promql-to-struct")
	defer span.End(&err)

	// 解析请求 body
	promQL := &structured.QueryPromQL{}
	err = json.NewDecoder(c.Request.Body).Decode(promQL)
	if err != nil {
		resp.failed(ctx, metadata.NewMessage(
			metadata.MsgTransformPromQL,
			"json 格式解析异常",
		).Error(ctx, err))
		return
	}

	promQLStr, _ := json.Marshal(promQL)
	span.Set("promql-body", string(promQLStr))

	query, err := promQLToStruct(ctx, promQL)
	if err != nil {
		resp.failed(ctx, metadata.NewMessage(
			metadata.MsgTransformPromQL,
			"转换查询结构异常",
		).Error(ctx, err))
		return
	}

	queryStr, _ := json.Marshal(query)
	span.Set("query-body", string(queryStr))

	resp.success(ctx, gin.H{"data": query})
}

// HandlerStructToPromQL
// @Summary  query struct to promql
// @ID       transform_struct_to_promql
// @Produce  json
// @Param    traceparent            header    string                          false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                          false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                          false  "空间UID" default(bkcc__2)
// @Param    data                  	body      structured.QueryTs  			  true   "json data"
// @Success  200                   	{object}  structured.QueryPromQL
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/ts/struct_to_promql [post]
func HandlerStructToPromQL(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		resp = &response{
			c: c,
		}

		err error
	)

	// 这里开始context就使用trace生成的了
	ctx, span := trace.NewSpan(ctx, "handle-struct-to-promql")
	defer span.End(&err)

	// 解析请求 body
	query := &structured.QueryTs{}
	err = json.NewDecoder(c.Request.Body).Decode(query)
	if err != nil {
		resp.failed(ctx, metadata.NewMessage(
			metadata.MsgTransformTs,
			"json 格式解析异常",
		).Error(ctx, err))
		return
	}
	queryStr, _ := json.Marshal(query)
	span.Set("query-body", string(queryStr))

	promQL, err := structToPromQL(ctx, query)
	if err != nil {
		resp.failed(ctx, metadata.NewMessage(
			metadata.MsgTransformTs,
			"转换查询结构异常",
		).Error(ctx, err))
		return
	}

	promQLStr, _ := json.Marshal(promQL)
	span.Set("promql-body", string(promQLStr))

	resp.success(ctx, promQL)
}

// HandlerQueryExemplar 查询时序 exemplar 数据
// @Summary  query monitor by ts exemplar
// @ID       query_ts_exemplar
// @Produce  json
// @Param    traceparent            header    string                        false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                        false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                   body      structured.QueryTs  			true   "json data"
// @Success  200                    {object}  PromData
// @Failure  400                    {object}  ErrResponse
// @Router   /query/ts/exemplar [post]
func HandlerQueryExemplar(c *gin.Context) {
	var (
		ctx = c.Request.Context()

		resp = &response{
			c: c,
		}
		user = metadata.GetUser(ctx)

		err error
	)

	ctx, span := trace.NewSpan(ctx, "handler-query-exemplar")
	defer span.End(&err)

	span.Set("request-url", c.Request.URL.String())
	span.Set("request-header", c.Request.Header)

	span.Set("query-source", user.Key)
	span.Set("query-space-uid", user.SpaceUID)
	span.Set("query-tenant-id", user.TenantID)

	// 解析请求 body
	query := &structured.QueryTs{}
	err = json.NewDecoder(c.Request.Body).Decode(query)
	if err != nil {
		resp.failed(ctx, metadata.NewMessage(
			metadata.MsgQueryExemplar,
			"json 格式解析异常",
		).Error(ctx, err))
		return
	}

	// metadata 中的 spaceUid 是从 header 头信息中获取
	if user.SpaceUID != "" {
		query.SpaceUid = user.SpaceUID
	}
	queryStr, _ := json.Marshal(query)
	span.Set("query-body", string(queryStr))

	metadata.NewMessage(
		metadata.MsgQueryExemplar,
		"%s, header: %+v, data: %+v",
		c.Request.URL.String(), c.Request.Header, string(queryStr),
	).Info(ctx)

	res, err := queryExemplar(ctx, query)
	if err != nil {
		resp.failed(ctx, metadata.NewMessage(
			metadata.MsgHandlerAPI,
			"查询异常",
		).Error(ctx, err))
		return
	}

	span.Set("resp-size", fmt.Sprint(unsafe.Sizeof(res)))
	resp.success(ctx, res)
}

// HandlerQueryRaw
// @Summary query monitor by raw data
// @ID query_raw
// @Produce json
// @Param    traceparent            header    string                        false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                        false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      structured.QueryTs  			true   "json data"
// @Success  200                   	{object}  PromData
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/raw [post]
func HandlerQueryRaw(c *gin.Context) {
	var (
		ctx      = c.Request.Context()
		resp     = &response{c: c}
		user     = metadata.GetUser(ctx)
		err      error
		span     *trace.Span
		listData ListData
	)

	ctx, span = trace.NewSpan(ctx, "handler-query-raw")
	defer func() {
		span.End(&err)
	}()

	span.Set("request-url", c.Request.URL.String())
	span.Set("request-header", c.Request.Header)

	span.Set("query-source", user.Key)
	span.Set("query-tenant-id", user.TenantID)
	span.Set("query-space-uid", user.SpaceUID)

	// 解析请求 body
	queryTs := &structured.QueryTs{}
	err = json.NewDecoder(c.Request.Body).Decode(queryTs)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	// metadata 中的 spaceUid 是从 header 头信息中获取
	if user.SpaceUID != "" {
		queryTs.SpaceUid = user.SpaceUID
	}

	queryStr, _ := json.Marshal(queryTs)
	span.Set("query-body", string(queryStr))

	listData.TraceID = span.TraceID()

	listData.Total, listData.List, listData.ResultTableOptions, err = queryRawWithInstance(ctx, queryTs)
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	// 避免空切片被解析成 null 的问题
	if listData.List == nil {
		listData.List = make([]map[string]any, 0)
	}
	if listData.ResultTableOptions == nil {
		listData.ResultTableOptions = make(metadata.ResultTableOptions)
	}

	resp.success(ctx, listData)
}

// HandlerQueryRawWithScroll
// @Summary query monitor by raw data with scroll
// @ID query_raw_with_scroll
// @Produce json
// @Param    traceparent            header    string                        false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                        false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      structured.QueryTs  			true   "json data"
// @Success  200                   	{object}  PromData
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/raw_with_scroll [post]
func HandlerQueryRawWithScroll(c *gin.Context) {
	var (
		ctx      = c.Request.Context()
		resp     = &response{c: c}
		user     = metadata.GetUser(ctx)
		err      error
		span     *trace.Span
		listData ListData
		session  *redis.ScrollSession
	)

	ctx, span = trace.NewSpan(ctx, "handler-query-raw-with-scroll")
	defer func() {
		if err != nil {
			resp.failed(ctx, metadata.NewMessage(
				metadata.MsgQueryRawScroll,
				"下载接口异常",
			).Error(ctx, err))
		}

		span.End(&err)
	}()

	span.Set("request-url", c.Request.URL.String())
	span.Set("request-header", c.Request.Header)

	span.Set("query-source", user.Key)
	span.Set("query-tenant-id", user.TenantID)
	span.Set("query-space-uid", user.SpaceUID)

	queryTs := &structured.QueryTs{}
	err = json.NewDecoder(c.Request.Body).Decode(queryTs)
	if err != nil {
		return
	}

	if user.SpaceUID != "" {
		queryTs.SpaceUid = user.SpaceUID
	}

	if queryTs.Scroll == "" {
		queryTs.Scroll = ScrollWindowTimeout
	}
	if queryTs.Limit == 0 {
		queryTs.Limit = ScrollSliceLimit
	}

	// 把是否清理的标记位提取出来，避免后续生成的 key 不一致
	clearCache := queryTs.ClearCache
	queryTs.ClearCache = false
	queryByte, _ := json.Marshal(queryTs)
	queryStr := string(queryByte)
	queryStrWithUserName := fmt.Sprintf("%s:%s", user.Name, queryStr)
	session, err = redis.GetOrCreateScrollSession(ctx, queryStrWithUserName, ScrollWindowTimeout, ScrollSessionLockTimeout, queryTs.SliceMax, queryTs.Limit)
	if err != nil {
		return
	}

	span.Set("query-body", queryStr)

	if clearCache {
		span.Set("clear-cache", "true")
		err = session.Clear(ctx)
		if err != nil {
			return
		}
		// 清理后需要重新初始化 session，确保从头开始查询
		// 重新创建一个新的 session 来替换被清理的 session
		session, err = redis.GetOrCreateScrollSession(ctx, queryStrWithUserName, ScrollWindowTimeout, ScrollSessionLockTimeout, queryTs.SliceMax, queryTs.Limit)
		if err != nil {
			return
		}
	}

	sessionStr, _ := json.Marshal(session)
	span.Set("session-object", sessionStr)

	span.Set("session-lock-key", queryStrWithUserName)
	listData.TraceID = span.TraceID()
	listData.Total, listData.List, listData.ResultTableOptions, listData.Done, err = queryRawWithScroll(ctx, queryTs, session)
	if err != nil {
		return
	}

	// 避免空切片被解析成 null 的问题
	if listData.List == nil {
		listData.List = make([]map[string]any, 0)
	}
	if listData.ResultTableOptions == nil {
		listData.ResultTableOptions = make(metadata.ResultTableOptions)
	}
	resp.success(ctx, listData)
}

// HandlerQueryTs
// @Summary  query monitor by ts
// @ID       query_ts
// @Produce  json
// @Param    traceparent            header    string                        false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                        false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      structured.QueryTs  			true   "json data"
// @Success  200                   	{object}  PromData
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/ts [post]
func HandlerQueryTs(c *gin.Context) { //gzl：处理结构体查询请求
	//gzl：1. 初始化阶段
	var (
		ctx = c.Request.Context()

		resp = &response{
			c: c,
		}
		user = metadata.GetUser(ctx)

		err error
	)
	//gzl：2. 链路追踪设置
	//创建分布式追踪span
	//记录请求URL、请求头、用户信息等关键元数据
	ctx, span := trace.NewSpan(ctx, "handler-query-ts")
	defer span.End(&err)

	span.Set("request-url", c.Request.URL.String())
	span.Set("request-header", c.Request.Header)

	span.Set("query-source", user.Key)
	span.Set("query-space-uid", user.SpaceUID)
	span.Set("query-tenant-id", user.TenantID)

	//gzl：3. 请求体解析
	//解析JSON格式的请求体到structured.QueryTs结构体
	//处理JSON解析异常，返回错误响应
	// 解析请求 body
	query := &structured.QueryTs{}
	err = json.NewDecoder(c.Request.Body).Decode(query)
	if err != nil {
		resp.failed(ctx, metadata.NewMessage(
			metadata.MsgQueryTs,
			"json 格式解析异常",
		).Error(ctx, err))
		return
	}

	//gzl：4. 空间UID处理
	// metadata 中的 spaceUid 是从 header 头信息中获取，header 如果有的话，覆盖参数里的
	if user.SpaceUID != "" {
		query.SpaceUid = user.SpaceUID
	}
	queryStr, _ := json.Marshal(query)
	span.Set("query-body", string(queryStr))
	span.Set("query-body-size", len(queryStr))

	metadata.NewMessage(
		metadata.MsgQueryTs,
		"%s, header: %+v, data: %+v",
		c.Request.URL.String(), c.Request.Header, string(queryStr),
	).Info(ctx)

	//gzl：5. 查询执行
	//·调用核心查询引擎queryTsWithPromEngine
	//·传入上下文和结构化查询参数
	//·执行实际的时序数据查询逻辑
	res, err := queryTsWithPromEngine(ctx, query) //todo：gzl step 1
	if err != nil {
		resp.failed(ctx, err)
		return
	}

	//gzl：6. 响应返回
	//·记录响应数据大小
	//·返回成功的JSON响应给客户端
	span.Set("resp-size", fmt.Sprint(unsafe.Sizeof(res)))

	resp.success(ctx, res)
}

// HandlerQueryPromQL gzl：处理 PromQL 查询请求
// @Summary  query monitor by promql
// @ID       query_promql
// @Produce  json
// @Param    traceparent            header    string                        false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                        false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      structured.QueryPromQL  		true   "json data"
// @Success  200                   	{object}  PromData
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/promql [post]
func HandlerQueryPromQL(c *gin.Context) { //gzl：通过PromQL语法查询监控数据
	var (
		ctx = c.Request.Context()

		resp = &response{
			c: c,
		}
		user = metadata.GetUser(ctx)

		err error
	)

	ctx, span := trace.NewSpan(ctx, "handler-query-promql")
	defer span.End(&err)

	span.Set("headers", c.Request.Header)
	span.Set("query-source", user.Key)
	span.Set("query-space-uid", user.SpaceUID)
	span.Set("query-tenant-id", user.TenantID)

	// 解析请求 body
	queryPromQL := &structured.QueryPromQL{}
	err = json.NewDecoder(c.Request.Body).Decode(queryPromQL)
	if err != nil {
		resp.failed(ctx, metadata.NewMessage(
			metadata.MsgParserPromQL,
			"json 格式解析异常",
		).Error(ctx, err))
		return
	}

	queryStr, _ := json.Marshal(queryPromQL)
	span.Set("query-body", string(queryStr))
	span.Set("query-promql", queryPromQL.PromQL)

	metadata.NewMessage(
		metadata.MsgParserPromQL,
		"%s, header: %+v, data: %+v",
		c.Request.URL.String(), c.Request.Header, string(queryStr),
	).Info(ctx)

	if queryPromQL.PromQL == "" {
		resp.failed(ctx, metadata.NewMessage(
			metadata.MsgQueryPromQL,
			"查询语句不能为空",
		).Error(ctx, err))
		return
	}

	//2、语法转换
	//·调用 promQLToStruct 函数将PromQL语法转换为结构化查询（HandlerQueryTs）
	//·进行语法验证和错误处理
	// promql to struct
	query, err := promQLToStruct(ctx, queryPromQL)
	if err != nil {
		resp.failed(ctx, metadata.NewMessage(
			metadata.MsgParserPromQL,
			"PromQL 语法解析异常",
		).Error(ctx, err))
		return
	}

	//3、查询执行
	//·调用 queryTsWithPromEngine 函数执行实际的时序数据查询
	//·该函数支持多种时序数据库后端（InfluxDB、Prometheus等）
	res, err := queryTsWithPromEngine(ctx, query)
	if err != nil {
		resp.failed(ctx, err)
		return
	}
	resp.success(ctx, res)
}

// HandlerQueryReference
// @Summary  query monitor by reference
// @ID       query_reference
// @Produce  json
// @Param    traceparent            header    string                        false  "TraceID" default(00-3967ac0f1648bf0216b27631730d7eb9-8e3c31d5109e78dd-01)
// @Param    Bk-Query-Source   		header    string                        false  "来源" default(username:goodman)
// @Param    X-Bk-Scope-Space-Uid   header    string                        false  "空间UID" default(bkcc__2)
// @Param	 X-Bk-Scope-Skip-Space  header	  string						false  "是否跳过空间验证" default()
// @Param    data                  	body      structured.QueryTs  			true   "json data"
// @Success  200                   	{object}  PromData
// @Failure  400                   	{object}  ErrResponse
// @Router   /query/reference [post]
func HandlerQueryReference(c *gin.Context) {
	var (
		ctx = c.Request.Context()

		resp = &response{
			c: c,
		}
		user = metadata.GetUser(ctx)
		err  error
	)

	ctx, span := trace.NewSpan(ctx, "handler-query-reference")
	defer span.End(&err)

	span.Set("request-url", c.Request.URL.String())
	span.Set("request-header", c.Request.Header)

	span.Set("query-source", user.Key)
	span.Set("query-space-uid", user.SpaceUID)
	span.Set("query-tenant-id", user.TenantID)

	// 解析请求 body
	query := &structured.QueryTs{}
	err = json.NewDecoder(c.Request.Body).Decode(query)
	if err != nil {
		resp.failed(ctx, metadata.NewMessage(
			metadata.MsgQueryReference,
			"json 格式解析异常",
		).Error(ctx, err))
		return
	}

	// metadata 中的 spaceUid 是从 header 头信息中获取
	if user.SpaceUID != "" {
		query.SpaceUid = user.SpaceUID
	}

	queryStr, _ := json.Marshal(query)
	span.Set("query-body", string(queryStr))
	span.Set("query-body-size", len(queryStr))

	metadata.NewMessage(
		metadata.MsgQueryReference,
		"%s, header: %+v, data: %+v",
		c.Request.URL.String(), c.Request.Header, string(queryStr),
	).Info(ctx)

	res, err := queryReferenceWithPromEngine(ctx, query)
	if err != nil {
		resp.failed(ctx, metadata.NewMessage(
			metadata.MsgQueryReference,
			"查询异常",
		).Error(ctx, err))
		return
	}
	if res != nil {
		span.Set("resp-table-length", len(res.Tables))
		span.Set("resp-size", fmt.Sprint(unsafe.Sizeof(res)))
	}

	resp.success(ctx, res)
}

// HandleInfluxDBPrint  打印 InfluxDB 路由信息
func HandleInfluxDBPrint(c *gin.Context) {
	ctx := c.Request.Context()
	refresh := c.Query("refresh")

	res := influxdbRouter.GetInfluxDBRouter().Print(ctx, refresh != "")
	c.String(200, res)
}

func HandlerQueryTsClusterMetrics(c *gin.Context) {
	var (
		ctx = c.Request.Context()

		resp = &response{c: c}

		err error
	)
	ctx, span := trace.NewSpan(ctx, "handler-query-ts-cluster-metrics")
	defer span.End(&err)

	span.Set("request-url", c.Request.URL.String())
	span.Set("request-header", c.Request.Header)
	query := &structured.QueryTs{}
	err = json.NewDecoder(c.Request.Body).Decode(query)
	if err != nil {
		resp.failed(ctx, metadata.NewMessage(
			metadata.MsgQueryClusterMetrics,
			"json 格式解析异常",
		).Error(ctx, err))
		return
	}
	queryStr, _ := json.Marshal(query)

	metadata.NewMessage(
		metadata.MsgQueryClusterMetrics,
		"%s, header: %+v, data: %+v",
		c.Request.URL.String(), c.Request.Header, string(queryStr),
	).Info(ctx)

	span.Set("query-body", string(queryStr))
	res, err := QueryTsClusterMetrics(ctx, query)
	if err != nil {
		resp.failed(ctx, metadata.NewMessage(
			metadata.MsgQueryClusterMetrics,
			"查询异常",
		).Error(ctx, err))
		return
	}
	resp.success(ctx, res)
}
