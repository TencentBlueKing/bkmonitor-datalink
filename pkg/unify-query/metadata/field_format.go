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
	"unicode"
)

type FieldFormat struct {
	ctx context.Context
}

func (f *FieldFormat) format(s string) string {
	return fmt.Sprintf("__bk_%s__", s)
}

func (f *FieldFormat) isAlphaNumericUnderscore(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == ':' || r == '_'
}

// EncodeFunc 对字段进行 Prom 引擎计算规则转换
func (f *FieldFormat) EncodeFunc() func(q string) string {
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

// DecodeFunc 对字段进行还原，别名无需还原，保留原字段
func (f *FieldFormat) DecodeFunc() func(q string) string {
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

func (f *FieldFormat) set() *FieldFormat {
	if md != nil {
		md.set(f.ctx, FieldFormatKey, f)
	}
	return f
}

func GetFieldFormat(ctx context.Context) *FieldFormat {
	if md != nil {
		r, ok := md.get(ctx, FieldFormatKey)
		if ok {
			if f, ok := r.(*FieldFormat); ok {
				return f
			}
		}
	}

	return (&FieldFormat{
		ctx: ctx,
	}).set()
}
