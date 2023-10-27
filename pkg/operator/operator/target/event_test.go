// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package target

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestEventTarget(t *testing.T) {
	target := EventTarget{
		DataID: 123,
		Labels: map[string]string{"event": "normal"},
	}

	ConfEventScrapeFiles = []string{"/path/to/file"}
	ConfEventScrapeInterval = "1m"
	ConfEventMaxSpan = "2h"

	b, err := target.YamlBytes()
	assert.NoError(t, err)

	excepted := `type: kubeevent
name: event_collect
version: "1"
task_id: 1
dataid: 123
interval: 1m
event_span: 2h
tail_files:
- /path/to/file
labels:
- event: normal
`

	assert.Equal(t, excepted, string(b))
	assert.Equal(t, "kubernetes-event.conf", target.FileName())
}
