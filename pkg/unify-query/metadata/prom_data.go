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
	"strings"
	"sync"
	"unicode"
)

type PromDataFormat struct {
	ctx  context.Context
	lock sync.RWMutex

	transformFormat string
	transformMap    map[rune]struct{}
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
		for _, r := range q {
			if !f.isAlphaNumericUnderscore(r) {
				invalidChars = append(invalidChars, r)
				result.WriteString(fmt.Sprintf(f.transformFormat, r))
			} else {
				result.WriteRune(r)
			}
		}

		f.lock.Lock()
		for _, r := range invalidChars {
			if _, ok := f.transformMap[r]; !ok {
				f.transformMap[r] = struct{}{}
			}
		}
		f.lock.Unlock()

		return result.String()
	}
}

func (f *PromDataFormat) DecodeFunc() func(q string) string {
	return func(q string) string {
		f.lock.RLock()
		defer f.lock.RUnlock()

		for k := range f.transformMap {
			q = strings.Replace(q, fmt.Sprintf(f.transformFormat, k), string(k), -1)
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
		ctx:             ctx,
		transformFormat: "__bk_%d__",
		transformMap:    make(map[rune]struct{}),
	}).set()
}
