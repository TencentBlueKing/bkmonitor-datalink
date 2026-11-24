// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// Draining a queued HTTP/1 body enables net/http's EOF background read, which
// observes a disconnected peer and cancels Request.Context. No graph work or
// execution slot is acquired here.
func readTopologyRequestBody(ctx context.Context, body io.Reader, writer http.ResponseWriter, maxBytes int) ([]byte, error) {
	return readLimitedRequestBody(ctx, body, writer, maxBytes, requestBodyLimit{span: "topology-read-request-body", subject: "topology request", reason: "max_topology_request_bytes"})
}

// requestBodyLimit 区分入口：各入口的原始输入规格使用自己的 trace 名称和超限原因。
type requestBodyLimit struct {
	span, subject, reason string
}

// readLimitedRequestBody 最多读取 maxBytes+1 字节，超过上限在解码前拒绝；取消时中断未完成的网络读。
func readLimitedRequestBody(ctx context.Context, body io.Reader, writer http.ResponseWriter, maxBytes int, limit requestBodyLimit) (data []byte, err error) {
	// 已取消或已释放的请求不再读取请求体。
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	_, span := trace.NewSpan(ctx, limit.span)
	defer func() {
		span.Set("request-body-bytes", len(data))
		span.Set("request-body-byte-limit", maxBytes)
		v1beta3.SetTimeGraphLimitTrace(span, err)
		span.End(&err)
	}()
	// Do not replace a server's existing ReadTimeout with a later deadline.
	// On cancellation only, interrupt an unfinished network read. Gin unwraps
	// its writer to net/http, which supports this control. An in-memory writer
	// may not; such a reader has no network-cancellation guarantee.
	controller := http.NewResponseController(writer)
	interrupted := make(chan struct{})
	var interruptErr error
	stop := context.AfterFunc(ctx, func() {
		interruptErr = controller.SetReadDeadline(time.Now())
		close(interrupted)
	})
	defer func() {
		if !stop() {
			<-interrupted
			span.Set("request-body-cancel-control-supported", interruptErr == nil)
			if interruptErr != nil {
				span.Set("request-body-cancel-control-error", interruptErr.Error())
			}
		}
	}()
	if maxBytes > 0 && maxBytes < int(^uint(0)>>1) {
		body = io.LimitReader(body, int64(maxBytes)+1)
	}
	data, err = io.ReadAll(body)
	if ctxErr := ctx.Err(); ctxErr != nil {
		return data, ctxErr
	}
	if err == nil && maxBytes > 0 && len(data) > maxBytes {
		err = fmt.Errorf("%s exceeds maximum size of %d bytes: %w", limit.subject, maxBytes, &v1beta3.ResultLimitError{Reason: limit.reason, Count: len(data), Limit: maxBytes})
	}
	return data, err
}

// decodeRelationRequest 按兼容入口的原始输入规格读取并解码请求：请求体超过
// max_relation_request_bytes 在解码前拒绝，批次超过 max_relation_queries 在执行前拒绝，
// 两者都使整个 HTTP 请求失败。与旧解码一致，只读取请求体中的第一个 JSON 值。
func decodeRelationRequest(ctx context.Context, c *gin.Context, request any, queryCount func() int) error {
	body, err := readLimitedRequestBody(ctx, c.Request.Body, c.Writer, v1beta3.RelationRequestByteLimit(), requestBodyLimit{
		span: "relation-read-request-body", subject: "relation request", reason: "max_relation_request_bytes",
	})
	if err != nil {
		return err
	}
	if err = json.NewDecoder(bytes.NewReader(body)).Decode(request); err != nil {
		return err
	}
	if limit, count := v1beta3.RelationQueryLimit(), queryCount(); limit > 0 && count > limit {
		return fmt.Errorf("relation request has too many queries: %w", &v1beta3.ResultLimitError{Reason: "max_relation_queries", Count: count, Limit: limit})
	}
	return nil
}
