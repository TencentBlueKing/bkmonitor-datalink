// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb

import (
	"bytes"
	"encoding/gob"
	"io"
	"net/http"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

// DumpsBackendData : 负责将传入的内容写入到指定的string中并返回
func DumpsBackendData(output io.Writer, original *Data) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	encoder := gob.NewEncoder(output)
	err := encoder.Encode(*original)
	if err != nil {
		flowLog.Errorf("failed  to encode info for->[%#v]", err)
		return err
	}
	return nil
}

// LoadsBackendData :
func LoadsBackendData(input io.Reader, result *Data) error {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module": moduleName,
	})
	decoder := gob.NewDecoder(input)
	err := decoder.Decode(result)
	if err != nil {
		flowLog.Errorf("failed to decode info for->[%#v]", err)
		return err
	}
	return nil
}

func backupDataToBuffer(flow uint64, urlParams *backend.WriteParams, query string, header http.Header) (*bytes.Buffer, error) {
	flowLog := logging.NewEntry(map[string]interface{}{
		"module":  moduleName,
		"flow_id": flow,
	})
	saveContentBuffer := bytes.NewBufferString("")
	backupData := &Data{
		Header:    header,
		URLParams: urlParams,
		Query:     string(query),
		FlowID:    flow,
	}
	if err := DumpsBackendData(saveContentBuffer, backupData); err != nil {
		flowLog.Errorf("failed to dumps data->[%v] for->[%v]", backupData, err)
		return nil, err
	}

	return saveContentBuffer, nil
}
