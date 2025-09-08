// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// NOTE: 优先使用 `http` 状态码

package http

import (
	"fmt"

	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/common"
)

// BindJSON bind http params to obj
func BindJSON(c *gin.Context, obj any) error {
	return c.ShouldBindWith(obj, binding.JSON)
}

// MergeGinH : merger h2 to h1
func MergeGinH(h1 *gin.H, h2 *gin.H) *gin.H {
	if h2 == nil {
		return h1
	}
	for key, value := range *h2 {
		(*h1)[key] = value
	}
	return h1
}

// GetMessage : return candidate if format is empty
func GetMessage(candidate string, format string, v []any) string {
	if format == "" {
		return candidate
	}
	if len(v) > 0 {
		return fmt.Sprintf(format, v...)
	}
	return format
}

// Response 正常返回
func Response(c *gin.Context, h *gin.H) {
	// 默认状态码为 200
	status := 200
	response := MergeGinH(&gin.H{
		"result":  true,
		"message": "ok",
		"code":    common.Success,
		"data":    nil,
	}, h)
	c.JSON(status, response)
}

// ResponseWithMessage 返回数据
func ResponseWithMessage(c *gin.Context, h any, message string, v ...any) {
	response := &gin.H{
		"result":  true,
		"code":    0,
		"message": GetMessage("ok", message, v),
		"data":    h,
	}
	Response(c, response)
}

// BadReqResponse return a bad request response
func BadReqResponse(c *gin.Context, message string, v ...any) {
	status := 400
	response := &gin.H{
		"result":  false,
		"code":    common.ParamsError,
		"message": GetMessage("bad request", message, v),
		"data":    nil,
	}
	c.JSON(status, response)
}

// ServerErrResponse return a error response
func ServerErrResponse(c *gin.Context, message string, v ...any) {
	status := 500
	response := &gin.H{
		"result":  false,
		"code":    common.ParamsError,
		"message": GetMessage("bad request", message, v),
		"data":    nil,
	}
	c.JSON(status, response)
}
