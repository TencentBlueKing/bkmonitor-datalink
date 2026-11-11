// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/service/http/proxy"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const topologyErrorMessageMaxBytes = 4096

const topologyInvalidRequestReason = "invalid_request"

func truncateTopologyErrorMessage(message string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	message = strings.ToValidUTF8(message, "\uFFFD")
	if len(message) <= maxBytes {
		return message
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(message[cut]) {
		cut--
	}
	return message[:cut]
}

// HandlerAPIRelationV1Beta3Topology 查询单个时间点的完整局部拓扑。
// 该接口与旧 path/multi_resource 接口分离，返回每个快照的节点和关系。
// @Summary  query relation shared topology
// @ID       relation_shared_topology_query_v1beta3
// @Description Direct relation endpoint. Send X-Bk-Scope-Space-Uid for routed queries; it has no built-in default. JWT authorization is conditional on server configuration. HTTP 200 still requires checking each data item code. See docs/api/timegraph.md for time, empty/partial, and migration contracts.
// @Accept json
// @Produce  json
// @Param    traceparent            header    string                         false  "TraceID"
// @Param X-Bkapi-Jwt header string false "Gateway-signed JWT; required when jwt.enabled is true. BasicAuth is not enforced by these relation handlers."
// @Param X-Bk-Scope-Space-Uid header string false "Routing space UID; no default. Required for physical routing and when configured JWT space authorization requires it."
// @Param    data                  body      cmdb.SharedTopologyRequest      true   "json data"
// @Success  200                   {object}  SharedTopologyResponseDocumentation
// @Failure 401 {object} ErrResponse "JWT authorization failed when enabled"
// @Failure  400                   {object}  ErrResponse
// @Router   /api/v1/relation/v1beta3/topology [post]
func HandlerAPIRelationV1Beta3Topology(c *gin.Context) {
	handleAPIRelationV1Beta3Topology(c, false)
}

// HandlerAPIRelationV1Beta3TopologyRange 查询统一时间网格上的完整局部拓扑。
// @Summary  query relation shared topology range
// @ID       relation_shared_topology_query_range_v1beta3
// @Description Direct relation endpoint. Send X-Bk-Scope-Space-Uid for routed queries; it has no built-in default. JWT authorization is conditional on server configuration. HTTP 200 still requires checking each data item code. See docs/api/timegraph.md for time, empty/partial, and migration contracts.
// @Accept json
// @Produce  json
// @Param    traceparent            header    string                         false  "TraceID"
// @Param X-Bkapi-Jwt header string false "Gateway-signed JWT; required when jwt.enabled is true. BasicAuth is not enforced by these relation handlers."
// @Param X-Bk-Scope-Space-Uid header string false "Routing space UID; no default. Required for physical routing and when configured JWT space authorization requires it."
// @Param    data                  body      cmdb.SharedTopologyRequest      true   "json data"
// @Success  200                   {object}  SharedTopologyResponseDocumentation
// @Failure 401 {object} ErrResponse "JWT authorization failed when enabled"
// @Failure  400                   {object}  ErrResponse
// @Router   /api/v1/relation/v1beta3/topology_range [post]
func HandlerAPIRelationV1Beta3TopologyRange(c *gin.Context) {
	handleAPIRelationV1Beta3Topology(c, true)
}

func handleAPIRelationV1Beta3Topology(c *gin.Context, rangeQuery bool) {
	// A batch can outlive the global metadata TTL. Keep its identity and routing
	// metadata request-owned through every item and the final response write.
	c.Request = c.Request.WithContext(metadata.WithQueryMetadata(c.Request.Context()))
	proxy.TraceJSONEncoding(c)
	spanName := "handler-api-relation-v1beta3-topology"
	if rangeQuery {
		spanName += "-range"
	}
	ctx, span := trace.NewSpan(c.Request.Context(), spanName)
	var handlerErr error
	resp := &response{c: c}
	queryMode := metric.CMDBRelationQueryModeInstant
	if rangeQuery {
		queryMode = metric.CMDBRelationQueryModeRange
	}
	started := time.Now()
	requestResult := metric.CMDBRelationResultSuccess
	finishRequest := func() {
		if writeErr := proxy.ResponseWriteError(c); handlerErr == nil && writeErr != nil {
			handlerErr = writeErr
		}
		if handlerErr != nil && requestResult != metric.CMDBRelationResultRejected {
			requestResult = metric.CMDBTimeGraphErrorResult(handlerErr)
		}
		metric.CMDBTopologyObserve(ctx, metric.CMDBTopologyScopeRequest, queryMode, requestResult, time.Since(started))
		v1beta3.SetTimeGraphLimitTrace(span, handlerErr)
		span.End(&handlerErr)
	}
	if _, proxied := c.Get(proxy.ContextConfigUnifyResponseProcess); proxied {
		proxy.DeferResponseCleanup(c, finishRequest)
	} else {
		defer finishRequest()
	}
	span.Set("query-mode", queryMode)
	span.Set("handler-headers", c.Request.Header)

	maxRequestBytes := v1beta3.TopologyRequestByteLimit()
	var body []byte
	bodyRead := false
	// 只有限长错误响应脱离取消；查询和成功响应（含代理最终写出）仍保留准入期限。
	fail := func(failureCtx context.Context, failure error) {
		failureCtx = context.WithoutCancel(failureCtx)
		c.Request = c.Request.WithContext(failureCtx)
		resp.failed(failureCtx, metadata.ErrorWithCauses(truncateTopologyErrorMessage(failure.Error(), topologyErrorMessageMaxBytes), failure))
	}
	// 准入和请求体拒绝保持原有的无效请求状态码。
	failBeforeQuery := func(failureCtx context.Context, failure error) {
		fail(failureCtx, errors.New(failure.Error()))
	}
	prepareBody := func(readCtx context.Context) error {
		if bodyRead {
			return nil
		}
		bodyRead = true
		var readErr error
		body, readErr = readTopologyRequestBody(readCtx, c.Request.Body, c.Writer, maxRequestBytes)
		if readErr != nil {
			requestResult = metric.CMDBRelationResultRejected
			reason := topologyInvalidRequestReason
			var limit interface{ TruncationReason() string }
			if errors.As(readErr, &limit) {
				reason = limit.TruncationReason()
			}
			metric.CMDBTopologyRejectInc(readCtx, queryMode, reason)
			metric.CMDBTopologyPayloadObserve(readCtx, "request-body", metric.CMDBRelationResultRejected, len(body))
		}
		return readErr
	}
	ctx, release, err := v1beta3.AcquireSharedTopologyWithQueuedPreparation(ctx, prepareBody)
	if err != nil {
		handlerErr = err
		var limit interface{ TruncationReason() string }
		if requestResult != metric.CMDBRelationResultRejected && errors.As(err, &limit) {
			requestResult = metric.CMDBRelationResultRejected
			metric.CMDBTopologyRejectInc(ctx, queryMode, limit.TruncationReason())
		}
		// Admission has already canceled/released its lease. Report the bounded
		// failure rather than silently producing an empty HTTP 200; no query or
		// success payload runs under this context. Socket WriteTimeout remains.
		failBeforeQuery(ctx, err)
		return
	}
	// Keep the admission deadline through the proxy's final segmented write.
	c.Request = c.Request.WithContext(ctx)
	if _, proxied := c.Get(proxy.ContextConfigUnifyResponseProcess); proxied {
		proxy.DeferResponseCleanup(c, func() {
			// Gin pools the Context without resetting its Keys at request exit.
			// Drop the encoded payload after proxy writing, before releasing its lease.
			c.Set(proxy.ContextKeyResponseData, nil)
			release()
		})
	} else {
		defer release()
	}
	if err := prepareBody(ctx); err != nil {
		handlerErr = err
		failBeforeQuery(ctx, err)
		return
	}
	request := new(cmdb.SharedTopologyRequest)
	_, decodeSpan := trace.NewSpan(ctx, "topology-decode-request")
	err = json.Unmarshal(body, request)
	decodeSpan.Set("query-count", len(request.QueryList))
	decodeSpan.End(&err)
	span.Set("request-body-bytes", len(body))
	if err != nil {
		handlerErr = err
		requestResult = metric.CMDBRelationResultRejected
		reason := topologyInvalidRequestReason
		metric.CMDBTopologyRejectInc(ctx, queryMode, reason)
		metric.CMDBTopologyPayloadObserve(ctx, "request-body", metric.CMDBRelationResultRejected, len(body))
		fail(ctx, err)
		return
	}
	metric.CMDBTopologyPayloadObserve(ctx, "request-body", metric.CMDBRelationResultSuccess, len(body))
	maxQueries := v1beta3.TopologyQueryLimit()
	if maxQueries > 0 && len(request.QueryList) > maxQueries {
		handlerErr = &v1beta3.ResultLimitError{Reason: "max_topology_queries", Count: len(request.QueryList), Limit: maxQueries}
		requestResult = metric.CMDBRelationResultRejected
		metric.CMDBTopologyRejectInc(ctx, queryMode, "max_topology_queries")
		fail(ctx, handlerErr)
		return
	}
	span.Set("query-count", len(request.QueryList))
	metric.CMDBRelationQueryListSizeObserve(ctx, metric.CMDBRelationRouteTimeGraph, queryMode, len(request.QueryList))
	_, modelSpan := trace.NewSpan(ctx, "topology-get-model")
	model, err := v1beta3.GetModel(ctx)
	modelSpan.End(&err)
	if err != nil {
		handlerErr = err
		fail(ctx, err)
		return
	}
	stream := newTopologyResponseStream(ctx, c, span.TraceID())
	// 批次逐项编码，编码完的项立即写出并释放，批次只保留正在编码的一项；累计字节只用于观测，
	// 不是拒绝理由。
	responseBytes := 0
	defer func() { span.Set("response-encoded-bytes", responseBytes) }()
	failedQueryCount := 0
	partialQueryCount := 0
	itemSpanName := "handler-api-relation-v1beta3-topology-item"
	if rangeQuery {
		itemSpanName += "-range"
	}
	finish := func(abortErr error) {
		span.Set("query-count", len(request.QueryList))
		span.Set("failed-query-count", failedQueryCount)
		span.Set("successful-query-count", len(request.QueryList)-failedQueryCount)
		span.Set("partial-failure", failedQueryCount > 0)
		switch {
		case len(request.QueryList) == 0:
			requestResult = metric.CMDBRelationResultEmpty
		case failedQueryCount == len(request.QueryList):
			requestResult = metric.CMDBRelationResultFailed
		case failedQueryCount > 0 || partialQueryCount > 0:
			requestResult = metric.CMDBRelationResultPartial
		}
		// 写出失败由 ResponseWriteError 带回请求终态。
		_ = stream.close()
		resp.streamed(ctx, abortErr)
	}
	// abort 处理批次中途的请求级失败（期限、取消、编码失败）。尚未写出时整个请求失败；
	// 已开始写出时 HTTP 200 和前面的项已经发出，当前项及其后各项按单项失败写出，响应仍是完整 JSON。
	abort := func(from int, failure error) {
		handlerErr = failure
		if !stream.started() {
			fail(ctx, failure)
			return
		}
		stream.detach()
		message := truncateTopologyErrorMessage(failure.Error(), topologyErrorMessageMaxBytes)
		for index := from; index < len(request.QueryList); index++ {
			failedQueryCount++
			parts, _, encodeErr := marshalTopologyItem(context.WithoutCancel(ctx), topologyFailedItem(request.QueryList[index].ResponseFormat, message))
			if encodeErr == nil {
				encodeErr = stream.item(parts)
			}
			if encodeErr != nil {
				break
			}
		}
		finish(failure)
	}
	for index, query := range request.QueryList {
		if err := ctx.Err(); err != nil {
			abort(index, err)
			return
		}
		queryStarted := time.Now()
		queryCtx, querySpan := trace.NewSpan(ctx, itemSpanName)
		query.SpaceUID = metadata.GetUser(ctx).SpaceUID
		querySpan.Set("query-index", index)
		querySpan.Set("requested-source-type", query.SourceType)
		querySpan.Set("requested-target-types", query.TargetTypes)
		querySpan.Set("requested-max-hops", query.MaxHops)
		querySpan.Set("requested-source-matcher-count", len(query.SourceInfo))
		querySpan.Set("requested-relation-type-count", len(query.AllowedRelationTypes))
		querySpan.Set("requested-category-count", len(query.AllowedCategories))
		querySpan.Set("requested-timestamp", query.Timestamp)
		querySpan.Set("requested-start", query.StartTime)
		querySpan.Set("requested-end", query.EndTime)
		querySpan.Set("requested-step", query.Step)
		var queryErr error
		if rangeQuery {
			if query.Timestamp != 0 {
				queryErr = fmt.Errorf("range topology query does not accept timestamp")
			} else if query.StartTime == 0 || query.EndTime == 0 {
				queryErr = fmt.Errorf("range topology query requires start_time and end_time")
			}
		} else if query.StartTime != 0 || query.EndTime != 0 {
			queryErr = fmt.Errorf("instant topology query does not accept start_time or end_time")
		} else if query.Timestamp == 0 {
			queryErr = fmt.Errorf("instant topology query requires timestamp")
		}
		var encoded topologyEncodedItem
		if queryErr == nil {
			// snapshots 在模型内逐帧构造并立即编码，不同时物化本项的全部帧。
			queryErr = model.QuerySharedTopologyFrames(queryCtx, query, func(header cmdb.SharedTopologyResult, frames v1beta3.TopologyFrames) error {
				encoded = encodeTopologyItem(queryCtx, query.ResponseFormat, header, frames, "")
				return encoded.err
			})
			if encoded.err != nil {
				queryErr = nil
			}
		} else {
			// HTTP 时间模式校验未进入模型，单独记录该子查询，避免遗漏或重复计数。
			metric.CMDBTopologyObserve(queryCtx, metric.CMDBTopologyScopeQuery, queryMode, metric.CMDBRelationResultRejected, time.Since(queryStarted))
			metric.CMDBTopologyRejectInc(queryCtx, queryMode, topologyInvalidRequestReason)
		}
		if queryErr != nil {
			failedQueryCount++
			encoded = encodeTopologyItem(queryCtx, query.ResponseFormat, cmdb.SharedTopologyResult{}, nil, truncateTopologyErrorMessage(queryErr.Error(), topologyErrorMessageMaxBytes))
		}
		querySpan.Set("encoded-item-bytes", encoded.bytes)
		if encoded.err != nil {
			// 编码失败（取消、期限）是请求级失败；已开始写出时当前项也按单项失败写出。
			encodeErr := encoded.err
			querySpan.End(&encodeErr)
			abort(index, encodeErr)
			return
		}
		responseBytes += encoded.bytes
		querySpan.Set("query-index", index)
		querySpan.Set("point-count", encoded.points)
		querySpan.Set("response-code", encoded.code)
		querySpan.Set("snapshot-count", encoded.snapshots)
		querySpan.Set("response-node-count", encoded.counts.nodes)
		querySpan.Set("response-edge-count", encoded.counts.edges)
		querySpan.Set("partial-snapshot-count", encoded.counts.partial)
		if encoded.versions {
			querySpan.Set("node-version-count", encoded.nodeVersions)
			querySpan.Set("edge-version-count", encoded.edgeVersions)
		}
		if encoded.counts.partial > 0 {
			partialQueryCount++
		}
		v1beta3.SetTimeGraphLimitTrace(querySpan, queryErr)
		querySpan.End(&queryErr)
		if err := stream.item(encoded.parts); err != nil {
			// 客户端已不可写：停止批次，写出错误由 ResponseWriteError 带回。
			finish(nil)
			return
		}
		encoded.parts = nil
	}
	finish(nil)
}

// topologyItemCounts 是一项输出的节点、边出现次数与 partial 帧数。
type topologyItemCounts struct{ nodes, edges, partial int }

// topologyEncodedItem 是一项编码后的片段及其观测数据；片段写出后即释放。
type topologyEncodedItem struct {
	parts                      [][]byte
	bytes                      int
	err                        error
	code, points, snapshots    int
	counts                     topologyItemCounts
	versions                   bool
	nodeVersions, edgeVersions int
}

func topologyFailedItem(format, message string) any {
	if format == cmdb.CompactTopologyFormat {
		return cmdb.CompactTopologyResponseData{Code: http.StatusBadRequest, Message: message, ResponseFormat: cmdb.CompactTopologyFormat}
	}
	return cmdb.SharedTopologyResponseData{Code: http.StatusBadRequest, Message: message, Snapshots: make([]cmdb.SharedTopologySnapshot, 0)}
}

// encodeTopologyItem 编码一项。message 非空表示失败项；snapshots 格式的成功项从 frames 逐帧编码。
func encodeTopologyItem(ctx context.Context, format string, header cmdb.SharedTopologyResult, frames v1beta3.TopologyFrames, message string) (encoded topologyEncodedItem) {
	_, encodeSpan := trace.NewSpan(ctx, "timegraph-encode-topology-item")
	encodeStarted := time.Now()
	defer func() {
		encodeSpan.Set("encoded-item-bytes", encoded.bytes)
		encodeSpan.Set("segmented", len(encoded.parts) > 0)
		encodeSpan.End(&encoded.err)
		metric.CMDBTimeGraphStageObserve(ctx, "topology-encode", metric.CMDBTimeGraphErrorResult(encoded.err), time.Since(encodeStarted))
		metric.CMDBTopologyPayloadObserve(ctx, "response-item", metric.CMDBTimeGraphErrorResult(encoded.err), encoded.bytes)
	}()
	encoded.code = http.StatusOK
	if message != "" {
		encoded.code = http.StatusBadRequest
		encoded.parts, encoded.bytes, encoded.err = marshalTopologyItem(ctx, topologyFailedItem(format, message))
		return encoded
	}
	encoded.points = header.PointCount
	if format == cmdb.CompactTopologyFormat {
		if header.Compact != nil {
			encoded.counts = topologyItemCounts{nodes: header.Compact.NodeOccurrences, edges: header.Compact.EdgeOccurrences, partial: len(header.Compact.Partial)}
			encoded.versions, encoded.nodeVersions, encoded.edgeVersions = true, len(header.Compact.Nodes), len(header.Compact.Edges)
		}
		encoded.parts, encoded.bytes, encoded.err = marshalTopologyItem(ctx, cmdb.CompactTopologyResponseData{
			Code: encoded.code, StartTime: header.StartTime, EndTime: header.EndTime, Step: header.Step,
			PointCount: header.PointCount, ResponseFormat: cmdb.CompactTopologyFormat, Compact: header.Compact,
		})
		return encoded
	}
	item := cmdb.SharedTopologyResponseData{Code: encoded.code, StartTime: header.StartTime, EndTime: header.EndTime, Step: header.Step, PointCount: header.PointCount}
	source := topologySnapshotSlice(header.Snapshots)
	count := len(header.Snapshots)
	if frames != nil {
		count = header.PointCount
		source = func(yield func(cmdb.SharedTopologySnapshot) error) error { return frames(yield) }
	}
	encoded.parts, encoded.bytes, encoded.err = marshalTopologySnapshotItem(ctx, item, count, func(yield func(cmdb.SharedTopologySnapshot) error) error {
		return source(func(snapshot cmdb.SharedTopologySnapshot) error {
			encoded.snapshots++
			encoded.counts.nodes += len(snapshot.Nodes)
			encoded.counts.edges += len(snapshot.Edges)
			if snapshot.Partial {
				encoded.counts.partial++
			}
			return yield(snapshot)
		})
	})
	return encoded
}
