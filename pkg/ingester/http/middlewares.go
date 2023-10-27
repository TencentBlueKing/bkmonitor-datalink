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
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/config"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/ingester/utils"
)

func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		receiverID := c.Param("receiverID")
		token := c.GetHeader(config.Configuration.Http.AuthHeader)

		r := GetReceiver(receiverID)
		if r == nil {
			c.AbortWithStatusJSON(http.StatusNotFound,
				define.NewHttpResponseFailed(c, fmt.Errorf("plugin(%s) does not exist", receiverID)))
			return
		}

		if !r.CheckAuth(token) {
			c.AbortWithStatusJSON(http.StatusUnauthorized,
				define.NewHttpResponseFailed(c,
					fmt.Errorf("invalid token, please provide `%s` header correctly",
						config.Configuration.Http.AuthHeader)))
			return
		}

		c.Next()
	}
}

func RequestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		define.InitRequestID(c)
		c.Next()
	}
}

func ErrorHandler() gin.HandlerFunc {
	return func(c *gin.Context) {
		defer utils.RecoverError(func(e error) {
			c.AbortWithStatusJSON(http.StatusInternalServerError, define.NewHttpResponseFailed(c, e))
		})
		c.Next()
	}
}
