// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2026 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

// Package textregexp 统一策略条件、自定义文本匹配与实例过滤的正则语义。
package textregexp

import (
	"errors"
	"strings"
	"time"

	"github.com/dlclark/regexp2/v2"
)

// ErrTimeout 表示本次匹配超时，不携带原始告警文本。
var ErrTimeout = errors.New("regular expression match timed out")

func executionError(err error) error {
	if err == nil || errors.Is(err, regexp2.ErrBacktrackingStackLimit) {
		return err
	}
	// regexp2 的超时错误包含整个输入，不能进入告警日志或持久 trace。
	if strings.HasPrefix(err.Error(), "match timeout") {
		return ErrTimeout
	}
	return errors.New("regular expression execution failed")
}

// Expression 是可并发复用的已编译表达式；选项在编译后不再修改。
type Expression struct {
	compiled *regexp2.Regexp
}

// Match 保留整段、捕获组及未参与匹配的分组（nil），区分空捕获与缺失。
type Match struct {
	Text        string
	Groups      []any
	NamedGroups map[string]any
}

// Compile 使用 .NET 风格、Unicode 和按出现顺序编号的捕获组。
// 超时由引擎检查，精度受其时钟周期影响，不承诺严格的实时截止时间。
func Compile(pattern string) (*Expression, error) {
	re, err := regexp2.Compile(pattern, regexp2.OptionMaintainCaptureOrder())
	if err != nil {
		return nil, err
	}
	re.MatchTimeout = 100 * time.Millisecond
	return &Expression{compiled: re}, nil
}

// NumSubexp 返回不含整段匹配的捕获组数量。
func (e *Expression) NumSubexp() int { return len(e.compiled.GetGroupNumbers()) - 1 }

// MatchString 查找任意位置的匹配，并保留超时等执行错误。
func (e *Expression) MatchString(text string) (bool, error) {
	matched, err := e.compiled.MatchString(text)
	return matched, executionError(err)
}

// FindAll 按文本顺序返回最多 limit 个不重叠匹配；调用方可多取一项判断超限。
func (e *Expression) FindAll(text string, limit int) ([]Match, error) {
	deadline := time.Now().Add(e.compiled.MatchTimeout)
	m, err := e.compiled.FindStringMatch(text)
	var out []Match
	for m != nil && err == nil && len(out) < limit {
		if time.Now().After(deadline) {
			return nil, ErrTimeout
		}
		groups := m.Groups()
		item := Match{Text: m.String(), Groups: make([]any, len(groups)-1), NamedGroups: map[string]any{}}
		for index, group := range groups[1:] {
			if len(group.Captures) == 0 {
				continue
			}
			item.Groups[index] = group.String()
			item.NamedGroups[group.Name] = group.String()
		}
		out = append(out, item)
		if len(out) == limit {
			break
		}
		m, err = e.compiled.FindNextMatch(m)
	}
	if err != nil {
		return nil, executionError(err)
	}
	return out, nil
}
