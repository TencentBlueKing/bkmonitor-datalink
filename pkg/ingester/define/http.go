// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/config"
)

// HttpResponse http 标准返回格式
type HttpResponse struct {
	Result    bool        `json:"result"`
	Data      interface{} `json:"data"`
	Message   string      `json:"message"`
	RequestID string      `json:"request_id"`
}

func NewHttpResponseSuccess(c *gin.Context, data interface{}) *HttpResponse {
	return &HttpResponse{
		Result:    true,
		Data:      data,
		RequestID: GetRequestID(c),
	}
}

func NewHttpResponseFailed(c *gin.Context, err error) *HttpResponse {
	return &HttpResponse{
		Result:    false,
		Message:   err.Error(),
		RequestID: GetRequestID(c),
	}
}

func InitRequestID(c *gin.Context) {
	// Get id from request
	rid := c.GetHeader(config.Configuration.Http.RequestIDHeader)
	if rid == "" {
		rid = uuid.New().String()
	}

	// Set the id to ensure that the requestid is in the response
	c.Header(config.Configuration.Http.RequestIDHeader, rid)
}

// GetRequestID returns the request identifier
func GetRequestID(c *gin.Context) string {
	return c.Writer.Header().Get(config.Configuration.Http.RequestIDHeader)
}
