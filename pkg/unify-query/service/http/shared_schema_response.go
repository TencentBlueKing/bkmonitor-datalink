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
	"reflect"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metric"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/query/structured"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

func (r *response) queryTsSharedSchemaNegotiation(query *structured.QueryTs) sharedSchemaNegotiation {
	if query.ResponseContract == structured.NamedOutputsV1 || r.isConfigUnifyRespProcess(r.c) {
		return sharedSchemaNegotiation{}
	}
	// Multiple Accept header lines have the same meaning as one joined list.
	return negotiateSharedSchema(strings.Join(r.c.Request.Header.Values("Accept"), ","), sharedSchemaV1Enabled.Load())
}

func (r *response) sharedSchemaSuccess(ctx context.Context, data *PromData) (err error) {
	started := time.Now()
	ctx, span := trace.NewSpan(ctx, "shared-schema-response")
	defer span.End(&err)
	plan, reason, err := preflightSharedSchema(ctx, data)
	preflightDuration := time.Since(started)
	var flush func() error
	if err == nil && reason == "" {
		var supported bool
		flush, supported = newSharedSchemaFlusher(r.c.Writer)
		if !supported {
			reason = "writer_unsupported"
		}
	}
	var stats sharedSchemaWriteStats
	result, codec, schemaCount := "fallback", "legacy-json", 0
	defer func() {
		metric.SharedSchemaResponseObserve(codec, result, reason, schemaCount, stats.Frames, stats.Bytes, preflightDuration, stats.EncodeDuration, stats.WriteDuration, time.Since(started))
		span.Set("response-codec", codec)
		span.Set("response-result", result)
		span.Set("response-reason", reason)
		span.Set("response-bytes", stats.Bytes)
		span.Set("response-frames", stats.Frames)
		span.Set("response-schemas", schemaCount)
	}()
	if err == nil && reason != "" {
		// Same owned query result; no additional query or transformation.
		legacyStarted := time.Now()
		r.success(ctx, data)
		stats.WriteDuration = time.Since(legacyStarted)
		if size := r.c.Writer.Size(); size > 0 {
			stats.Bytes = int64(size)
		}
		return nil
	}
	user := metadata.GetUser(ctx)
	if err == nil {
		codec = "shared-schema-v1"
		schemaCount = len(plan.schemas)
		r.c.Header("Content-Type", sharedSchemaV1MediaType)
		r.c.Status(http.StatusOK)
		stats, err = writeSharedSchema(ctx, r.c.Writer, flush, data, plan)
	}
	if err != nil {
		result, reason = "failure", stats.FailureStage
		if reason == "" {
			reason = "preflight_cancel"
		}
		metric.APIRequestInc(ctx, r.c.Request.URL.Path, metric.StatusFailed, user.SpaceUID, user.Source)
		// Headers may already be committed. Missing end/transport failure is
		// the stream failure signal; never append a legacy JSON error.
		return err
	}
	result, reason = "success", ""
	metric.APIRequestInc(ctx, r.c.Request.URL.Path, metric.StatusSuccess, user.SpaceUID, user.Source)
	return nil
}

// Gin's built-in writer has Unwrap, but its void Flush takes precedence over
// Unwrap in ResponseController and discards the underlying FlushError. Keep
// all writes and header accounting on Gin, and bypass that one flush layer.
// An outer error-aware wrapper owns its flush semantics and is kept intact;
// opaque void-only wrappers retain their legacy outlet before any bytes.
func newSharedSchemaFlusher(writer gin.ResponseWriter) (func() error, bool) {
	var target http.ResponseWriter = writer
	if _, ok := writer.(interface{ FlushError() error }); !ok {
		// Only Gin's pinned built-in layer is known to have no buffering
		// semantics beyond WriteHeaderNow. Never skip a custom wrapper's
		// void Flush merely because it also exposes Unwrap.
		typeOfWriter := reflect.TypeOf(writer)
		if typeOfWriter.Kind() != reflect.Pointer || typeOfWriter.Elem().PkgPath() != "github.com/gin-gonic/gin" || typeOfWriter.Elem().Name() != "responseWriter" {
			return nil, false
		}
		unwrapper, ok := writer.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil, false
		}
		target = unwrapper.Unwrap()
	}
	if !supportsSharedSchemaFlushError(target) {
		return nil, false
	}
	controller := http.NewResponseController(target)
	return func() error {
		writer.WriteHeaderNow()
		return controller.Flush()
	}, true
}

// Match ResponseController's dispatch order without executing a flush. A
// void-only flusher is an opaque boundary even when it also has Unwrap: its
// own flush can be meaningful (for example, compression) and cannot report
// errors. Only transparent layers without a flush method may be traversed.
func supportsSharedSchemaFlushError(writer http.ResponseWriter) bool {
	for {
		switch target := writer.(type) {
		case interface{ FlushError() error }:
			return true
		case http.Flusher:
			return false
		case interface{ Unwrap() http.ResponseWriter }:
			writer = target.Unwrap()
		default:
			return false
		}
	}
}

func (r *response) sharedSchemaNotAcceptable(ctx context.Context) {
	user := metadata.GetUser(ctx)
	metric.APIRequestInc(ctx, r.c.Request.URL.Path, metric.StatusFailed, user.SpaceUID, user.Source)
	r.c.JSON(http.StatusNotAcceptable, ErrResponse{
		TraceID: trace.TraceIDFromContext(ctx),
		Err:     "shared schema v1 requires application/json fallback in Accept",
	})
}
