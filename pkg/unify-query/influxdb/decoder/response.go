// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package decoder

import (
	"context"
)

// 不要重新生成，原来生成的不支持chunked，已经修改了
//go:generate msgp

// Message represents a user message.
type Message struct {
	Level string `json:"level,omitempty" msg:"level"`
	Text  string `json:"text,omitempty" msg:"text"`
}

// Row
type Row struct {
	Name    string            `json:"name,omitempty" msg:"name"`
	Tags    map[string]string `json:"tags,omitempty" msg:"tags"`
	Columns []string          `json:"columns,omitempty" msg:"columns"`
	Values  [][]any           `json:"values,omitempty" msg:"values"`
	Partial bool              `json:"partial,omitempty" msg:"partial"`
}

// Result
type Result struct {
	StatementID int        `json:"statement_id,omitempty" msg:"statement_id"`
	Series      []*Row     `json:"series,omitempty" msg:"series"`
	Messages    []*Message `json:"messages,omitempty" msg:"messages"`
	Partial     bool       `json:"partial,omitempty" msg:"partial"`
	Err         string     `json:"error,omitempty" msg:"error"`
}

// Response
type Response struct {
	Ctx     context.Context `json:"-"`
	Results []Result        `json:"results,omitempty" msg:"results"`
	Err     string          `json:"error,omitempty" msg:"error"`
}
