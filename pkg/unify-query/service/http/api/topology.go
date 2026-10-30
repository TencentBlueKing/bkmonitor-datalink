// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	stdjson "encoding/json"
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
	data := &topologyResponse{
		TraceID: span.TraceID(),
		Data:    make([]stdjson.RawMessage, len(request.QueryList)),
	}
	responseBytes := 1024
	responseOwnedBytes := topologyEncodedOwnership{}
	defer func() {
		span.Set("response-budget-bytes-used", responseBytes)
		span.Set("response-budget-bytes-limit", v1beta3.TopologyOutputByteLimit())
	}()
	failedQueryCount := 0
	partialQueryCount := 0
	itemSpanName := "handler-api-relation-v1beta3-topology-item"
	if rangeQuery {
		itemSpanName += "-range"
	}
	for index, query := range request.QueryList {
		if err := ctx.Err(); err != nil {
			handlerErr = err
			fail(ctx, err)
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
		item := cmdb.SharedTopologyResponseData{Code: http.StatusOK}
		var result cmdb.SharedTopologyResult
		var snapshotPlan *cmdb.SharedTopologySnapshotPlan
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
		if queryErr == nil {
			if v1beta3.YoloModeEnabled() && query.ResponseFormat != cmdb.CompactTopologyFormat {
				var planned cmdb.SharedTopologySnapshotPlanResult
				planned, queryErr = model.QuerySharedTopologySnapshotPlan(queryCtx, query)
				result = cmdb.SharedTopologyResult{StartTime: planned.StartTime, EndTime: planned.EndTime, Step: planned.Step, PointCount: planned.PointCount}
				snapshotPlan = planned.Plan
			} else {
				result, queryErr = model.QuerySharedTopology(queryCtx, query)
			}
		} else {
			// HTTP 时间模式校验未进入模型，单独记录该子查询，避免遗漏或重复计数。
			metric.CMDBTopologyObserve(queryCtx, metric.CMDBTopologyScopeQuery, queryMode, metric.CMDBRelationResultRejected, time.Since(queryStarted))
			metric.CMDBTopologyRejectInc(queryCtx, queryMode, topologyInvalidRequestReason)
		}
		if queryErr != nil {
			failedQueryCount++
			item.Code = http.StatusBadRequest
			item.Message = queryErr.Error()
		} else {
			item.StartTime = result.StartTime
			item.EndTime = result.EndTime
			item.Step = result.Step
			item.PointCount = result.PointCount
			item.Snapshots = result.Snapshots
		}
		if item.Snapshots == nil {
			item.Snapshots = make([]cmdb.SharedTopologySnapshot, 0)
		}
		// Record metadata before transferring private snapshot headers to encoding.
		snapshotCount := len(item.Snapshots)
		nodeCount, edgeCount, partialCount := 0, 0, 0
		for _, snapshot := range item.Snapshots {
			nodeCount += len(snapshot.Nodes)
			edgeCount += len(snapshot.Edges)
			if snapshot.Partial {
				partialCount++
			}
		}
		if result.Compact != nil {
			nodeCount, edgeCount, partialCount = result.Compact.NodeOccurrences, result.Compact.EdgeOccurrences, len(result.Compact.Partial)
			querySpan.Set("node-version-count", len(result.Compact.Nodes))
			querySpan.Set("edge-version-count", len(result.Compact.Edges))
		}
		if snapshotPlan != nil {
			snapshotCount = len(snapshotPlan.Timestamps)
			nodeCount = snapshotPlan.NodeOccurrences
			edgeCount = snapshotPlan.EdgeOccurrences
			partialCount = len(snapshotPlan.Partial)
			querySpan.Set("node-version-count", len(snapshotPlan.Nodes))
			querySpan.Set("edge-version-count", len(snapshotPlan.Edges))
		}
		// 批次内结果累计也必须有界，不能用 query_list 倍增单查询预算。
		item.Message = truncateTopologyErrorMessage(item.Message, topologyErrorMessageMaxBytes)
		_, encodeSpan := trace.NewSpan(queryCtx, "timegraph-encode-topology-item")
		encodeStarted := time.Now()
		var payload any = item
		if query.ResponseFormat == cmdb.CompactTopologyFormat {
			payload = cmdb.CompactTopologyResponseData{
				Code:           item.Code,
				StartTime:      item.StartTime,
				EndTime:        item.EndTime,
				Step:           item.Step,
				PointCount:     item.PointCount,
				Message:        item.Message,
				ResponseFormat: cmdb.CompactTopologyFormat,
				Compact:        result.Compact,
			}
		}
		// Reserve retained item buffers plus outer encoding/write buffers.
		encodeErr := ctx.Err()
		if encodeErr == nil && v1beta3.YoloModeEnabled() {
			encodeErr = v1beta3.ReserveTopologyStage(ctx, "encoded", int64(responseBytes+v1beta3.TopologyOutputByteLimit())*3)
		}
		var encoded []byte
		var chunks [][]byte
		encodedBytes := 0
		itemOwnedBytes := topologyEncodedOwnership{}
		if encodeErr == nil && !v1beta3.YoloModeEnabled() {
			chunks, encodedBytes, itemOwnedBytes, encodeErr = marshalTopologyBounded(queryCtx, payload, v1beta3.TopologyOutputByteLimit()-responseBytes, responseOwnedBytes)
		}
		if encodeErr == nil && v1beta3.YoloModeEnabled() {
			// The usual element budget already bounds small snapshots. Large
			// snapshots encode frame by frame to bound encoder buffers.
			var elements int
			for _, snapshot := range item.Snapshots {
				if encodeErr = ctx.Err(); encodeErr != nil {
					break
				}
				elements += len(snapshot.Nodes) + len(snapshot.Edges)
			}
			if encodeErr == nil && snapshotPlan != nil {
				chunks, encodedBytes, encodeErr = marshalTopologySnapshotPlan(queryCtx, item, snapshotPlan)
				snapshotPlan = nil
			} else if encodeErr == nil && query.ResponseFormat != cmdb.CompactTopologyFormat && elements > largeTopologyEncodingElementThreshold {
				// Only this private header copy is consumed. The model contract and
				// the general encoder remain non-destructive.
				item.Snapshots = append([]cmdb.SharedTopologySnapshot(nil), item.Snapshots...)
				result.Snapshots = nil
				chunks, encodedBytes, encodeErr = marshalOwnedTopologySnapshots(queryCtx, item)
				item.Snapshots = nil
			} else if encodeErr == nil {
				encoded, encodeErr = marshalTopologyItem(payload)
				encodedBytes = len(encoded)
				if encodeErr == nil {
					encodeErr = ctx.Err()
				}
			}
		}
		encodeSpan.Set("encoded-item-bytes", encodedBytes)
		encodeSpan.Set("segmented", len(chunks) > 0)
		encodeSpan.End(&encodeErr)
		metric.CMDBTimeGraphStageObserve(queryCtx, "topology-encode", metric.CMDBTimeGraphErrorResult(encodeErr), time.Since(encodeStarted))
		metric.CMDBTopologyPayloadObserve(queryCtx, "response-item", metric.CMDBTimeGraphErrorResult(encodeErr), encodedBytes)
		querySpan.Set("encoded-item-bytes", encodedBytes)
		if encodeErr != nil {
			querySpan.End(&encodeErr)
			handlerErr = encodeErr
			fail(ctx, encodeErr)
			return
		}
		if limit := v1beta3.TopologyOutputByteLimit(); limit > 0 && encodedBytes > limit-responseBytes {
			queryErr = &v1beta3.ResultLimitError{Reason: "max_topology_output_bytes", Count: responseBytes + encodedBytes, Limit: v1beta3.TopologyOutputByteLimit()}
			v1beta3.SetTimeGraphLimitTrace(querySpan, queryErr)
			querySpan.End(&queryErr)
			handlerErr = queryErr
			requestResult = metric.CMDBRelationResultRejected
			metric.CMDBTopologyRejectInc(ctx, queryMode, "max_topology_output_bytes")
			fail(ctx, queryErr)
			return
		}
		responseBytes += encodedBytes + 1
		item.Snapshots = nil
		result.Snapshots = nil
		result.Compact = nil
		_ = v1beta3.ReserveTopologyStage(ctx, "output", 0)
		if v1beta3.YoloModeEnabled() {
			_ = v1beta3.ReserveTopologyStage(ctx, "encoded", int64(responseBytes)*3)
		} else {
			responseOwnedBytes.reserved += itemOwnedBytes.reserved
			responseOwnedBytes.materialized += itemOwnedBytes.materialized
		}
		querySpan.Set("query-index", index)
		querySpan.Set("point-count", item.PointCount)
		querySpan.Set("response-code", item.Code)
		querySpan.Set("snapshot-count", snapshotCount)
		querySpan.Set("response-node-count", nodeCount)
		querySpan.Set("response-edge-count", edgeCount)
		querySpan.Set("partial-snapshot-count", partialCount)
		if partialCount > 0 {
			partialQueryCount++
		}
		v1beta3.SetTimeGraphLimitTrace(querySpan, queryErr)
		querySpan.End(&queryErr)
		data.Data[index] = encoded
		if len(chunks) > 0 {
			if data.chunks == nil {
				data.chunks = make([][][]byte, len(request.QueryList))
			}
			data.chunks[index] = chunks
		}
	}
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
	resp.success(ctx, data)
}
