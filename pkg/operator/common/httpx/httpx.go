// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package httpx

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/url"
	"sort"
	"strings"
)

func WindParams(params map[string]string) string {
	buf := &bytes.Buffer{}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := params[k]
		if v != "" {
			buf.WriteString(fmt.Sprintf("%s=%s&", k, v))
		}
	}

	s := strings.TrimRight(buf.String(), "&")
	if s == "" {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}

func UnwindParams(s string) url.Values {
	q, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return make(url.Values)
	}

	u, err := url.Parse("http://localhost:8080/parse?" + string(q))
	if err != nil {
		return make(url.Values)
	}
	return u.Query()
}
