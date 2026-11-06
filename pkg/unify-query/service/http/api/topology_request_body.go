// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/cmdb/v1beta3"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

// Draining a queued HTTP/1 body enables net/http's EOF background read, which
// observes a disconnected peer and cancels Request.Context. Only request-buffer
// reservation is held here; no graph work or execution slot is acquired.
func readTopologyRequestBody(ctx context.Context, body io.Reader, writer http.ResponseWriter, maxBytes int) (data []byte, err error) {
	if err = v1beta3.ReserveTopologyStage(ctx, "request", int64(maxBytes)*6); err != nil {
		return nil, err
	}
	_, span := trace.NewSpan(ctx, "topology-read-request-body")
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
		err = fmt.Errorf("topology request exceeds maximum size of %d bytes: %w", maxBytes, &v1beta3.ResultLimitError{Reason: "max_topology_request_bytes", Count: len(data), Limit: maxBytes})
	}
	return data, err
}
