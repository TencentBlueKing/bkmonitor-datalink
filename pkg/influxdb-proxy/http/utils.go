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
	"compress/gzip"
	"io/ioutil"
	"net/http"
	"regexp"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/influxdb-proxy/logging"
)

func readRequestBody(request *http.Request, flowLog *logging.Entry) ([]byte, error) {
	body := request.Body
	// 确认如果是压缩的方案，需要解压body
	if request.Header.Get("Content-Encoding") == "gzip" {
		flowLog.Tracef("write request in gzip, use unzip.")
		b, err := gzip.NewReader(request.Body)
		if err != nil {
			return nil, ErrGzipReadFailed
		}
		defer func() { _ = b.Close() }()
		body = b
	}
	data, err := ioutil.ReadAll(body)
	if err != nil {
		return nil, ErrReadBodyFailed
	}
	return data, nil
}

// RegexpMatch 匹配子字符串
func RegexpMatch(matchExp *regexp.Regexp, s string) []string {
	result := matchExp.FindStringSubmatch(s)
	if len(result) >= 1 {
		return result[1:]
	}
	return []string{}
}

// CheckSingleWord  检查输入的db是否是一个单词，如果中间有空格则是错误输入
func CheckSingleWord(db string) bool {
	// 单个单词匹配
	singleWordExp := regexp.MustCompile(`^\S+$`)
	if db == "" {
		return false
	}
	idx := singleWordExp.FindStringIndex(db)
	// 匹配不上singleWordExp证明不是single word，这时返回false
	if idx == nil {
		return false
	}
	// 匹配成功说明是singleword
	return true
}
