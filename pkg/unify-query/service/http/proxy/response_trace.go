// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/render"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

const responseWriteErrorKey = "unify_response_write_error"

const traceJSONEncodingKey = "unify_trace_json_encoding"

// TraceJSONEncoding opts the topology route into separate encode/write spans,
// including the final proxy envelope. Other routes retain their renderer.
func TraceJSONEncoding(c *gin.Context) { c.Set(traceJSONEncodingKey, true) }

type tracedJSON struct {
	ctx  context.Context
	data any
}

// JSONSegmenter supplies a complete JSON value as pre-encoded fragments.
// A nil result uses the regular encoder.
type JSONSegmenter interface {
	JSONSegments() ([][]byte, error)
}

func responseJSONSegments(data any) ([][]byte, error) {
	if segmented, ok := data.(JSONSegmenter); ok {
		return segmented.JSONSegments()
	}
	if envelope, ok := data.(*apiGwResponse); ok && envelope.Result && envelope.Message == SuccessMessage {
		if segmented, ok := envelope.Data.(JSONSegmenter); ok {
			parts, err := segmented.JSONSegments()
			if err != nil || len(parts) == 0 {
				return parts, err
			}
			result := make([][]byte, 0, len(parts)+2)
			result = append(result, []byte(`{"result":true,"data":`))
			result = append(result, parts...)
			return append(result, []byte(`,"message":"success"}`)), nil
		}
	}
	return nil, nil
}

func (r tracedJSON) WriteContentType(w http.ResponseWriter) {
	render.JSON{}.WriteContentType(w)
}

func (r tracedJSON) Render(w http.ResponseWriter) error {
	r.WriteContentType(w)
	_, encodeSpan := trace.NewSpan(r.ctx, "http-response-encode")
	var body []byte
	var parts [][]byte
	var size int
	err := r.ctx.Err()
	if err == nil {
		parts, err = responseJSONSegments(r.data)
		if err == nil {
			if len(parts) == 0 {
				body, err = json.Marshal(r.data)
				size = len(body)
			} else {
				for _, part := range parts {
					if len(part) > int(^uint(0)>>1)-size {
						err = errors.New("JSON response size overflows int")
						break
					}
					size += len(part)
				}
			}
		}
	}
	encodeSpan.Set("encoded-body-bytes", size)
	encodeSpan.Set("segmented", len(parts) > 0)
	encodeSpan.End(&err)
	if err != nil {
		return err
	}
	_, writeSpan := trace.NewSpan(r.ctx, "http-response-write")
	err = r.ctx.Err()
	var written int
	if err == nil {
		if len(parts) == 0 {
			written, err = w.Write(body)
			if err == nil && written != len(body) {
				err = io.ErrShortWrite
			}
		} else {
			for _, part := range parts {
				if err = r.ctx.Err(); err != nil {
					break
				}
				var n int
				n, err = w.Write(part)
				written += n
				if err == nil && n != len(part) {
					err = io.ErrShortWrite
				}
				if err != nil {
					break
				}
			}
		}
	}
	writeSpan.Set("writer-body-bytes", written)
	writeSpan.End(&err)
	return err
}

// WriteJSON 观测实际 JSON 编码与 ResponseWriter 写出，不把代理暂存响应视为已写出。
func WriteJSON(ctx context.Context, c *gin.Context, status int, data any) {
	_, span := trace.NewSpan(ctx, "http-response-encode-write")
	var err error
	defer span.End(&err)
	beforeErrors, beforeBytes := len(c.Errors), c.Writer.Size()
	if beforeBytes < 0 {
		beforeBytes = 0
	}
	if c.GetBool(traceJSONEncodingKey) {
		c.Render(status, tracedJSON{ctx: ctx, data: data})
	} else {
		c.JSON(status, data)
	}
	if len(c.Errors) > beforeErrors {
		err = c.Errors.Last().Err
		c.Set(responseWriteErrorKey, err)
	}
	span.Set("http-status", c.Writer.Status())
	span.Set("writer-body-bytes", c.Writer.Size()-beforeBytes)
	_, proxied := c.Get(ContextConfigUnifyResponseProcess)
	span.Set("proxy-response", proxied)
}

func ResponseWriteError(c *gin.Context) error {
	value, _ := c.Get(responseWriteErrorKey)
	err, _ := value.(error)
	return err
}
