// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package jfr

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestContext(t *testing.T) {
	ctx := &Context{
		Labels: map[int64]int64{
			1: 100,
			2: 200,
			3: 300,
		},
	}

	labelKey := int64(1)
	expectedLabelValue := int64(100)
	value, ok := ctx.Labels[labelKey]
	assert.True(t, ok)
	assert.Equal(t, value, expectedLabelValue)

	labelKey = int64(4)
	_, ok = ctx.Labels[labelKey]
	assert.False(t, ok)
}

func TestLabelsSnapshot(t *testing.T) {
	snapshot := &LabelsSnapshot{
		Contexts: map[int64]*Context{
			1: {
				Labels: map[int64]int64{
					1: 100,
					2: 200,
					3: 300,
				},
			},
			2: {
				Labels: map[int64]int64{
					4: 400,
					5: 500,
					6: 600,
				},
			},
		},
		Strings: map[int64]string{
			1: "string1",
			2: "string2",
			3: "string3",
		},
	}

	contextKey := int64(1)
	_, ok := snapshot.Contexts[contextKey]
	assert.True(t, ok)

	stringKey := int64(2)
	expectedStringValue := "string2"
	value, ok := snapshot.Strings[stringKey]
	assert.True(t, ok)
	assert.Equal(t, value, expectedStringValue)
}

func TestReset(t *testing.T) {
	t.Run("Context Reset", func(t *testing.T) {
		ctx := &Context{
			Labels: map[int64]int64{
				1: 100,
				2: 200,
				3: 300,
			},
		}

		ctx.Reset()

		assert.Nil(t, ctx.Labels)
	})

	t.Run("LabelsSnapshot Reset", func(t *testing.T) {
		snapshot := &LabelsSnapshot{
			Contexts: map[int64]*Context{
				1: {
					Labels: map[int64]int64{
						1: 100,
						2: 200,
						3: 300,
					},
				},
				2: {
					Labels: map[int64]int64{
						4: 400,
						5: 500,
						6: 600,
					},
				},
			},
			Strings: map[int64]string{
				1: "string1",
				2: "string2",
				3: "string3",
			},
		}

		snapshot.Reset()

		assert.Nil(t, snapshot.Contexts)
		assert.Nil(t, snapshot.Strings)
	})
}

func TestContextAndSnapshotMethods(t *testing.T) {
	t.Run("Context Methods", func(t *testing.T) {
		// Initialize a new context
		ctx := &Context{
			Labels: map[int64]int64{
				1: 100,
				2: 200,
				3: 300,
			},
		}
		assert.NotNil(t, ctx.ProtoReflect())
	})

	t.Run("LabelsSnapshot Methods", func(t *testing.T) {
		snapshot := &LabelsSnapshot{
			Contexts: map[int64]*Context{
				1: {
					Labels: map[int64]int64{
						1: 100,
						2: 200,
						3: 300,
					},
				},
			},
			Strings: map[int64]string{
				1: "string1",
				2: "string2",
				3: "string3",
			},
		}

		assert.NotNil(t, snapshot.ProtoReflect())
	})
}

func TestDescriptorMethods(t *testing.T) {
	t.Run("Context Methods", func(t *testing.T) {
		ctx := &Context{
			Labels: map[int64]int64{
				1: 100,
				2: 200,
				3: 300,
			},
		}

		expectedLabels := map[int64]int64{
			1: 100,
			2: 200,
			3: 300,
		}
		assert.Equal(t, expectedLabels, ctx.GetLabels())
	})

	t.Run("LabelsSnapshot Methods", func(t *testing.T) {
		snapshot := &LabelsSnapshot{
			Contexts: map[int64]*Context{
				1: {
					Labels: map[int64]int64{
						1: 100,
						2: 200,
						3: 300,
					},
				},
			},
			Strings: map[int64]string{
				1: "string1",
				2: "string2",
				3: "string3",
			},
		}

		expectedContexts := map[int64]*Context{
			1: {
				Labels: map[int64]int64{
					1: 100,
					2: 200,
					3: 300,
				},
			},
		}
		assert.Equal(t, expectedContexts, snapshot.GetContexts())

		expectedStrings := map[int64]string{
			1: "string1",
			2: "string2",
			3: "string3",
		}
		assert.Equal(t, expectedStrings, snapshot.GetStrings())
	})
}
