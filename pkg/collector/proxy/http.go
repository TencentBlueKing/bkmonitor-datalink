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
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/pkg/errors"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/internal/utils"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

func (p *Proxy) V2PushRoute(w http.ResponseWriter, req *http.Request) {
	defer utils.HandleCrash()
	ip := utils.ParseRequestIP(req.RemoteAddr, req.Header)

	start := time.Now()
	buf := &bytes.Buffer{}
	_, err := io.Copy(buf, req.Body)
	if err != nil {
		logger.Errorf("failed to read prometheus exported content, error %s", err)
		DefaultMetricMonitor.IncInternalErrorCounter()
		writeResponse(w, err.Error(), http.StatusInternalServerError)
		return
	}
	bufLen := buf.Len()
	defer func() {
		_ = req.Body.Close()
	}()

	// 空请求体拒绝
	if bufLen <= 0 {
		err = errors.Errorf("empty request body not allowed, ip=%v", ip)
		logger.Warn(err)
		DefaultMetricMonitor.IncDroppedCounter(0, http.StatusBadRequest)
		writeResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	pd := define.ProxyData{}
	if err = json.Unmarshal(buf.Bytes(), &pd); err != nil {
		logger.Warnf("failed to parse proxy data, ip=%v, error %s", ip, err)
		DefaultMetricMonitor.IncDroppedCounter(pd.DataId, http.StatusBadRequest)
		writeResponse(w, err.Error(), http.StatusBadRequest)
		return
	}

	r := &define.Record{
		RequestType: define.RequestHttp,
		Token: define.Token{
			Original:    pd.AccessToken,
			ProxyDataId: int32(pd.DataId),
			AppName:     "proxy",
		},
		RequestClient: define.RequestClient{IP: ip},
		RecordType:    define.RecordProxy,
		Data:          &pd,
	}
	code, processorName, err := p.Validate(r)
	if err != nil {
		writeResponse(w, err.Error(), int(code))
		logger.Warnf("failed to run pre-check processors, code=%d, dataid=%v, ip=%v, error %s", code, pd.DataId, ip, err)
		DefaultMetricMonitor.IncPreCheckFailedCounter(processorName, r.Token.Original, pd.DataId, code)
		return
	}

	globalRecords.Push(r)
	recordMetrics(r.Token, start, bufLen)
	writeResponse(w, "", http.StatusOK)
}

func recordMetrics(token define.Token, t time.Time, n int) {
	DefaultMetricMonitor.AddReceivedBytesCounter(float64(n), int64(token.ProxyDataId))
	DefaultMetricMonitor.ObserveBytesDistribution(float64(n), int64(token.ProxyDataId))
	DefaultMetricMonitor.ObserveHandledDuration(t, int64(token.ProxyDataId))
	DefaultMetricMonitor.IncHandledCounter(int64(token.ProxyDataId))
	define.SetTokenInfo(token)
}

type responseData struct {
	Code    string `json:"code"`
	Result  string `json:"result"`
	Message string `json:"message"`
}

func writeResponse(w http.ResponseWriter, message string, code int) {
	w.WriteHeader(code)
	w.Header().Set("Content-Type", define.ContentTypeJson)

	result := "false"
	if code >= 200 && code < 300 {
		result = "true"
	}
	b, _ := json.Marshal(responseData{
		Code:    strconv.Itoa(code),
		Result:  result,
		Message: message,
	})
	w.Write(b)
}
