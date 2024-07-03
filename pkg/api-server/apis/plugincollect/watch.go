// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package plugincollect

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/api-server/apis/response"
	redisWatch "github.com/TencentBlueKing/bkmonitor-datalink/pkg/api-server/watch"
)

func Watch(c *gin.Context) {
	channel := c.Param("channel")

	watcher := redisWatch.NewWatcher(channel)
	sub := watcher.Watch()
	defer sub.Close()

	for {
		select {
		case msg := <-sub.Channel():
			c.String(http.StatusOK, "watch channel: %s\n", msg.Payload)
			if w, ok := c.Writer.(http.Flusher); ok {
				w.Flush()
			} else {
				return
			}
		case <-c.Request.Context().Done():
			response.NewSuccessResponse(c, nil)
		}
	}
}
