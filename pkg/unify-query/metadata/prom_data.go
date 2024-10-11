// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode"
)

type PromDataFormat struct {
	ctx  context.Context
	lock sync.RWMutex

	transformFormat string
}

func (f *PromDataFormat) format(s string) string {
	return fmt.Sprintf("__bk_%s__", s)
}

func (f *PromDataFormat) isAlphaNumericUnderscore(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}

func (f *PromDataFormat) EncodeFunc() func(q string) string {
	return func(q string) string {
		var (
			result       strings.Builder
			invalidChars []rune
		)
		format := f.format(`%d`)
		for _, r := range q {
			if !f.isAlphaNumericUnderscore(r) {
				invalidChars = append(invalidChars, r)
				result.WriteString(fmt.Sprintf(format, r))
			} else {
				result.WriteRune(r)
			}
		}

		return result.String()
	}
}

func (f *PromDataFormat) DecodeFunc() func(q string) string {
	return func(q string) string {
		format := f.format(`([\d]+)`)
		re := regexp.MustCompile(format)
		matchList := re.FindAllStringSubmatch(q, -1)
		for _, match := range matchList {
			if len(match) == 2 {
				num, err := strconv.Atoi(match[1])
				if err != nil {
					continue
				}
				q = strings.ReplaceAll(q, match[0], string(rune(num)))
			}
		}
		return q
	}
}

func (f *PromDataFormat) set() *PromDataFormat {
	if md != nil {
		md.set(f.ctx, PromDataFormatKey, f)
	}
	return f
}

func GetPromDataFormat(ctx context.Context) *PromDataFormat {
	if md != nil {
		r, ok := md.get(ctx, PromDataFormatKey)
		if ok {
			if f, ok := r.(*PromDataFormat); ok {
				return f
			}
		}
	}

	return (&PromDataFormat{
		ctx: ctx,
	}).set()
}
