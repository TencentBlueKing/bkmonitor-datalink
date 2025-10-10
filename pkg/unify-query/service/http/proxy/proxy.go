// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/trace"
)

type apiGwRequest struct {
	Path string          `json:"path"`
	Data json.RawMessage `json:"data"`
}

const (
	ContextKeyResponseData            = "responseData"
	ContextKeyResponseError           = "responseError"
	ContextConfigUnifyResponseProcess = "unify_response_process"

	SuccessMessage = "success"
)

type apiGwResponse struct {
	c       *gin.Context `json:"-"`
	Result  bool         `json:"result"`
	Data    any          `json:"data"`
	Message string       `json:"message"`
}

func (a *apiGwResponse) failed(msg error) {
	a.c.JSON(http.StatusBadRequest, &apiGwResponse{
		Result:  false,
		Data:    nil,
		Message: msg.Error(),
	})
}

func (a *apiGwResponse) success(data any) {
	a.c.JSON(http.StatusOK, &apiGwResponse{
		Result:  true,
		Data:    data,
		Message: SuccessMessage,
	})
}

func HandleProxy(c *gin.Context) {
	var (
		ctx  = c.Request.Context()
		resp = &apiGwResponse{c: c}
		err  error
	)

	ctx, span := trace.NewSpan(ctx, "handler-proxy")

	defer func() {
		if err != nil {
			_ = metadata.Sprintf(
				metadata.MsgHandlerAPI,
				"代理接口查询异常",
			).Error(ctx, err)
			resp.failed(err)
		}

		span.End(&err)
	}()

	span.Set("request-url", c.Request.URL.String())
	span.Set("request-header", c.Request.Header)
	query := &apiGwRequest{}
	err = json.NewDecoder(c.Request.Body).Decode(query)
	if err != nil {
		return
	}
	c.Set(ContextConfigUnifyResponseProcess, true)
	handlers, exist := metadata.GetHandler(query.Path)
	if !exist {
		c.Status(http.StatusNotFound)
		return
	}
	log.Debugf(ctx, "api gw request path: %s, data: %s", query.Path, query.Data)
	c.Request.Body = io.NopCloser(bytes.NewReader(query.Data))
	for _, h := range handlers {
		h(c)
	}
	responseError, hasError := c.Get(ContextKeyResponseError)
	responseData, _ := c.Get(ContextKeyResponseData)

	if hasError {
		apiErr, ok := responseError.(error)
		if !ok {
			apiErr = fmt.Errorf("response error type assertion failed, got: %T", responseError)
			err = apiErr
		} else {
			err = apiErr
		}
		return
	}
	resp.success(responseData)
}
