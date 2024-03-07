// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"bytes"
	"regexp"
	"strings"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/utils/logger"
)

const (
	MatchRegex         = "reg"
	MatchEqual         = "eq"
	MatchNotEqual      = "nq"
	MatchStartsWith    = "startswith"
	MatchNotStartsWith = "nstartswith"
	MatchEndsWith      = "endswith"
	MatchNotEndsWith   = "nendswith"
	MatchContains      = "in"
	MatchNotContains   = "nin"
	MatchWildcard      = "wildcard"
	MatchHex           = "hex"
)

// WildcardToRegex :
func WildcardToRegex(pattern string) string {
	pattern = regexp.QuoteMeta(pattern)
	replacements := map[string]string{
		`\?`: `.{1}`,
		`\*`: `.*`,
	}
	for old, new := range replacements {
		pattern = strings.Replace(pattern, old, new, -1)
	}
	return pattern
}

// IsMatchString :
func IsMatchString(matchType string, content string, expect string) bool {
	if len(expect) == 0 {
		return true
	}
	switch matchType {
	case MatchRegex:
		reg, err := regexp.Compile(expect)
		if err != nil {
			return false
		}
		return reg.FindString(content) != ""
	case MatchWildcard:
		return IsMatchString(MatchRegex, content, WildcardToRegex(expect))
	default:
		logger.Warnf("unknown match type: %v", matchType)
		return false
	}
}

// IsMatch :
func IsMatch(matchType string, content []byte, expect []byte) bool {
	if len(expect) == 0 {
		return true
	}

	switch matchType {
	case MatchContains:
		return bytes.Contains(content, expect)
	case MatchNotContains:
		return !bytes.Contains(content, expect)
	case MatchEqual:
		return bytes.Equal(content, expect)
	case MatchNotEqual:
		return !bytes.Equal(content, expect)
	case MatchStartsWith:
		return bytes.HasPrefix(content, expect)
	case MatchNotStartsWith:
		return !bytes.HasPrefix(content, expect)
	case MatchEndsWith:
		return bytes.HasSuffix(content, expect)
	case MatchNotEndsWith:
		return !bytes.HasSuffix(content, expect)
	case MatchHex:
		exceptBytes, err := ConvertHexStringToBytes(string(expect))
		if err != nil {
			logger.Warnf("decode hex string %v failed: %v", expect, err)
			return false
		}
		return bytes.Equal(content, exceptBytes)
	default:
		return IsMatchString(matchType, string(content), string(expect))
	}
}
