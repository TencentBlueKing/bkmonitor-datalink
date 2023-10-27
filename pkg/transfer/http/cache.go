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
	"io"
	"net/http"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/logging"
)

func init() {
	http.HandleFunc("/cache", func(writer http.ResponseWriter, request *http.Request) {
		/*
			return: cmdb 缓存数据;
			格式： {
				"result": ture,
				"data": map[string]*define.StoreItem,,
				"message": "xxxx"
			}
		*/

		rsp := new(define.RespCacheData)

		headerMethod := "X-Request-Method"
		headerSysError := "X-Server-Error"

		header := writer.Header()
		header.Add(headerMethod, request.Method)

		body, err := io.ReadAll(request.Body)
		if err != nil {
			logging.Errorf("read body error: %s", err)
			header.Add(headerSysError, err.Error())
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		defer func() {
			_ = request.Body.Close()
		}()

		reqData := make(map[string]interface{})

		if len(body) != 0 {
			err = json.Unmarshal(body, &reqData)
			if err != nil {
				logging.Errorf("unmarshal body:[%s] error: %s", body, err)
				writer.WriteHeader(http.StatusBadRequest)
			}
		}

		var targetType string
		var s define.Store
		storeType := reqData["store_type"]
		if val, ok := storeType.(string); val == "" || !ok {
			s, targetType = define.GetStore("")
		} else {
			s, targetType = define.GetStore(val)
		}

		// 试图转为支持内存缓存的接口
		ms, ok := s.(define.MemStore)
		if !ok {
			// unsupport type
			rsp.Result = false
			rsp.Message = fmt.Sprintf("unsurpport store type: %v", storeType)
			mesg, err := json.Marshal(rsp)
			if err != nil {
				writer.WriteHeader(http.StatusBadRequest)
			} else {
				writer.WriteHeader(http.StatusOK)
			}
			writer.Write(mesg)
			return
		}

		memData := make(map[string]*define.StoreItem)
		scanErr := ms.ScanMemData("", func(key string, data []byte) bool {
			logging.Debugf("scan memory data: %s", string(data))
			item := new(define.StoreItem)
			if innerErr := json.Unmarshal(data, item); innerErr != nil {
				logging.Warnf("error scanMemdata unmarshal: %s", innerErr)
			}
			memData[key] = item
			return true
		}, true)

		if scanErr != nil {
			logging.Warnf("error scanMemdata: %s", scanErr)
			err = scanErr
		}

		if err != nil {
			logging.Errorf("type: [%s] get mem cache err: [%s]", targetType, err)
			writer.WriteHeader(http.StatusBadRequest)
			rsp.Result = false
			rsp.Message = fmt.Sprintf("type:[%s], get mem cache err:[%s]", targetType, err)
			WriteJSONResponse(0, writer, rsp)
			return
		}

		rsp.Result = true
		rsp.Data = memData
		writer.WriteHeader(http.StatusOK)
		WriteJSONResponse(0, writer, rsp)
	})
}
