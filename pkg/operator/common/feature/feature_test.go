// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package feature

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestParseLabelJoinMatcher(t *testing.T) {
	cases := []struct {
		input       string
		annotations []string
		labels      []string
	}{
		{
			input:       "Pod://annotation:biz.service,annotation:biz.set,label:zone.key1,label:zone.key2",
			annotations: []string{"biz.service", "biz.set"},
			labels:      []string{"zone.key1", "zone.key2"},
		},
		{
			input:       "Pod:// annotation: biz.service, annotation: biz.set, label: zone.key1, label: zone.key2",
			annotations: []string{"biz.service", "biz.set"},
			labels:      []string{"zone.key1", "zone.key2"},
		},
	}

	for _, c := range cases {
		matcher := parseLabelJoinMatcher(c.input)
		assert.Equal(t, c.annotations, matcher.Annotations)
		assert.Equal(t, c.labels, matcher.Labels)
	}
}
