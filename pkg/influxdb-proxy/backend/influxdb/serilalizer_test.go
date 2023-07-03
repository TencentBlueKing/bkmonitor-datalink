// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package influxdb_test

import (
	"bytes"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/suite"
	"gotest.tools/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/backend/influxdb"
)

// SerSuite :
type SerSuite struct {
	suite.Suite
}

func (bs *SerSuite) TestUsage() {
	original := influxdb.Data{
		Header:    http.Header{"test": []string{"123"}},
		URLParams: backend.NewWriteParams("test_db", "", "", ""),
		Query:     "query data",
	}

	writeString := bytes.NewBufferString("")
	fmt.Println(writeString)
	_ = influxdb.DumpsBackendData(writeString, &original)
	backendString := string(writeString.Bytes())
	fmt.Println(backendString)
	var recovery influxdb.Data
	readString := strings.NewReader(backendString)
	_ = influxdb.LoadsBackendData(readString, &recovery)

	assert.Equal(bs.T(), recovery.URLParams.DB, original.URLParams.DB)
	assert.Equal(bs.T(), recovery.Query, original.Query)
	assert.Equal(bs.T(), recovery.Header["test"][0], original.Header["test"][0])
}

// TestSerSuite :
func TestSerSuite(t *testing.T) {
	suite.Run(t, new(SerSuite))
}
