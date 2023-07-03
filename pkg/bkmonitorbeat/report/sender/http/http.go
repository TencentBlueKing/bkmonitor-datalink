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
	"bytes"
	"fmt"
	"io"
	"net/http"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/report"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bkmonitorbeat/report/message"
)

func Register() {
	report.RegisterSender("http", func(config report.ReportConfig) (report.Sender, error) {
		if len(config.HTTPServer) == 0 {
			return nil, fmt.Errorf("config %s empty", report.ReportHTTPServerKey)
		}
		return &HttpSender{server: config.HTTPServer}, nil
	})
}

type HttpSender struct {
	server string
}

func (hs *HttpSender) GetUrl() string {
	return fmt.Sprintf("http://%s/v2/push/", hs.server)
}

func (hs *HttpSender) SendSync(bkDataID int64, msg *message.Message) error {
	return hs.Send(bkDataID, msg)
}

func (hs *HttpSender) Send(bkDataID int64, msg *message.Message) error {
	body := bytes.NewReader([]byte(msg.Content))
	rsp, err := http.Post(hs.GetUrl(), "application/json", body)
	if err != nil {
		return fmt.Errorf("http post failed, err: %+v", err)
	}
	defer rsp.Body.Close()
	if rsp.StatusCode != 200 {
		responseBody := make([]byte, 0)
		responseBody, err := io.ReadAll(rsp.Body)
		if err != nil {
			return fmt.Errorf("http post failed, status: %s, read response body failed, err: %+v", rsp.Status, err)
		}
		return fmt.Errorf("http post failed, status: %s, responseBody:%s", rsp.Status, string(responseBody))
	}

	_, err = io.ReadAll(rsp.Body)
	if err != nil {
		return fmt.Errorf("http post success, but read response body failed, err: %+v", err)
	}
	return nil
}
