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
	"io"
	"net/http"
	"regexp"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/eventbus"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
)

func init() {
	http.HandleFunc("/signal/", func(writer http.ResponseWriter, request *http.Request) {
		headerMethod := "X-Request-Method"
		headerSignal := "X-Signal-Name"
		headerSysError := "X-Server-Error"
		headerActivated := "X-Signal-Activated"

		header := writer.Header()
		header.Add(headerMethod, request.Method)
		if request.Method != "POST" {
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}

		signalPattern := regexp.MustCompile(`/signal/(?P<name>[\w\-]+)/?`)
		patterns := signalPattern.FindStringSubmatch(request.URL.Path)
		name, ok := define.GetSignalByName(patterns[1])

		header.Add(headerSignal, name)
		if name == "" {
			writer.WriteHeader(http.StatusForbidden)
			return
		}

		body, err := io.ReadAll(request.Body)
		if err != nil {
			header.Add(headerSysError, err.Error())
			writer.WriteHeader(http.StatusBadRequest)
			return
		}

		values := make(map[string]string)
		err = json.Unmarshal(body, &values)
		if err != nil {
			header.Add(headerSysError, err.Error())
			writer.WriteHeader(http.StatusBadRequest)
			return
		}

		if ok {
			eventbus.Publish(name, values)
		}
		header.Add(headerActivated, strconv.FormatBool(ok))
		writer.WriteHeader(http.StatusNoContent)
	})
}
