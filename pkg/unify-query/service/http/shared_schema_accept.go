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
	"mime"
	"net/http"
	"strconv"
	"strings"
)

const sharedSchemaV1MediaType = "application/vnd.bkmonitor.uq.shared-schema.v1+ndjson"

type sharedSchemaNegotiation struct {
	explicit bool
	selected bool
	reject   bool
}

// Only an explicit, acceptable v1 range opts in. Legacy must also be
// acceptable because preflight can select the same result's JSON outlet.
func negotiateSharedSchema(accept string, enabled bool) sharedSchemaNegotiation {
	var result sharedSchemaNegotiation
	v1Quality, jsonQuality, jsonSpecificity := -1.0, -1.0, -1
	for _, part := range splitAccept(accept) {
		mediaType, params, err := mime.ParseMediaType(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		quality := 1.0
		if q, ok := params["q"]; ok {
			quality, err = strconv.ParseFloat(q, 64)
			if err != nil || quality < 0 || quality > 1 || quality != quality {
				continue
			}
		}
		if mediaType == sharedSchemaV1MediaType {
			result.explicit = true
			if quality > v1Quality {
				v1Quality = quality
			}
		}
		specificity := -1
		switch mediaType {
		case "application/json":
			specificity = 2
		case "application/*":
			specificity = 1
		case "*/*":
			specificity = 0
		}
		if specificity >= 0 && (specificity > jsonSpecificity || specificity == jsonSpecificity && quality > jsonQuality) {
			jsonQuality, jsonSpecificity = quality, specificity
		}
	}
	result.reject = v1Quality > 0 && jsonQuality <= 0
	result.selected = enabled && v1Quality > 0 && jsonQuality > 0 && v1Quality >= jsonQuality
	return result
}

func splitAccept(value string) []string {
	var parts []string
	start, quoted, escaped := 0, false, false
	for i := 0; i < len(value); i++ {
		if escaped {
			escaped = false
			continue
		}
		switch value[i] {
		case '\\':
			escaped = quoted
		case '"':
			quoted = !quoted
		case ',':
			if !quoted {
				parts = append(parts, value[start:i])
				start = i + 1
			}
		}
	}
	return append(parts, value[start:])
}

func varyAccept(header http.Header) {
	for _, value := range header.Values("Vary") {
		for _, item := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(item), "Accept") || strings.TrimSpace(item) == "*" {
				return
			}
		}
	}
	header.Add("Vary", "Accept")
}
