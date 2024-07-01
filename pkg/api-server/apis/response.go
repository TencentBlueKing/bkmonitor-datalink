// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package apis

import (
	"net/http"

	"github.com/gin-gonic/gin"
)

var (
	SuccessCode = 0   // 成功
	ParamsError = 400 // 参数错误
	ServerErr   = 500 // 服务器错误
)

// RespFields response fields
type RespFields struct {
	Result  bool        `json:"result"`
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data"`
}

// ApiResponse api response fields
type ApiResponse struct {
	HttpCode int `json:"http_code"`
	Resp     RespFields
}

// NewResponse create a new response
func NewResponse(c *gin.Context, HttpCode int, result bool, code int, message string, data interface{}) {
	resp := RespFields{
		Result:  result,
		Code:    code,
		Message: message,
		Data:    data,
	}
	c.Keys = make(map[string]interface{})
	c.Keys["response"] = &ApiResponse{
		HttpCode: HttpCode,
		Resp:     resp,
	}
}

// NewSuccessResponse create a new success response
func NewSuccessResponse(c *gin.Context, data interface{}) {
	NewResponse(c, http.StatusOK, true, SuccessCode, "ok", data)
}

// NewParamsErrorResponse create a new params error response
func NewParamsErrorResponse(c *gin.Context, message string) {
	NewResponse(c, http.StatusBadRequest, false, ParamsError, message, nil)
}

// NewServerErrorResponse create a new server error response
func NewServerErrorResponse(c *gin.Context, message string) {
	NewResponse(c, http.StatusInternalServerError, false, ServerErr, message, nil)
}
