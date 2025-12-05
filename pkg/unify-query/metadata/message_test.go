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
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewMessage(t *testing.T) {
	tests := []struct {
		name   string
		id     string
		format string
		args   []any
		want   string
	}{
		{
			name:   "simple message",
			id:     "test_id",
			format: "test message",
			args:   nil,
			want:   "test message",
		},
		{
			name:   "message with format",
			id:     "test_id",
			format: "test %s message",
			args:   []any{"formatted"},
			want:   "test formatted message",
		},
		{
			name:   "message with multiple args",
			id:     "test_id",
			format: "test %s %d message",
			args:   []any{"formatted", 123},
			want:   "test formatted 123 message",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := NewMessage(tt.id, tt.format, tt.args...)
			assert.NotNil(t, msg)
			assert.Equal(t, tt.id, msg.ID)
			assert.Equal(t, tt.want, msg.Content)
		})
	}
}

func TestMessage_Text(t *testing.T) {
	msg := NewMessage("test_id", "test message")
	text := msg.Text()
	assert.Contains(t, text, "[test_id]")
	assert.Contains(t, text, "test message")
}

func TestMessage_String(t *testing.T) {
	msg := NewMessage("test_id", "test message")
	assert.Equal(t, "test message", msg.String())
}

func TestMessage_Error(t *testing.T) {
	ctx := context.Background()

	t.Run("error without wrapped error", func(t *testing.T) {
		msg := NewMessage("test_id", "test error")
		err := msg.Error(ctx, nil)
		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "test error")
	})

	t.Run("error with empty content", func(t *testing.T) {
		msg := NewMessage("test_id", "")
		err := msg.Error(ctx, nil)
		assert.NotNil(t, err)
		assert.Equal(t, "", err.Error())
	})

	t.Run("error with wrapped error", func(t *testing.T) {
		msg := NewMessage("test_id", "test error")
		originalErr := errors.New("original error")
		err := msg.Error(ctx, originalErr)
		assert.NotNil(t, err)
		assert.Contains(t, err.Error(), "test error")
		assert.Contains(t, err.Error(), "original error")
	})
}

func TestMessage_Warn(t *testing.T) {
	ctx := context.Background()
	msg := NewMessage("test_id", "test warning")
	// 这个函数只是记录日志，我们只验证它不会 panic
	assert.NotPanics(t, func() {
		msg.Warn(ctx)
	})
}

func TestMessage_Info(t *testing.T) {
	ctx := context.Background()
	msg := NewMessage("test_id", "test info")
	// 这个函数只是记录日志，我们只验证它不会 panic
	assert.NotPanics(t, func() {
		msg.Info(ctx)
	})
}

func TestMessage_Status(t *testing.T) {
	InitMetadata()
	ctx := context.Background()
	msg := NewMessage("test_id", "test status")
	// 这个函数设置状态并记录日志，我们只验证它不会 panic
	assert.NotPanics(t, func() {
		msg.Status(ctx, "test_code")
	})
	// 验证状态被设置
	status := GetStatus(ctx)
	assert.NotNil(t, status)
	assert.Equal(t, "test_code", status.Code)
	assert.Equal(t, "test status", status.Message)
}

func TestMessageConstants(t *testing.T) {
	// 验证所有消息常量都已定义
	constants := []string{
		MsgParserUnifyQuery,
		MsgParserSQL,
		MsgParserDoris,
		MsgParserLucene,
		MsgParserPromQL,
		MsgQueryRedis,
		MsgQueryES,
		MsgQueryVictoriaMetrics,
		MsgQueryBKSQL,
		MsgQueryInfluxDB,
		MsgTransformTs,
		MsgTransformPromQL,
		MsgQueryPromQL,
		MsgQueryRelation,
		MsgQueryInfo,
		MsgQueryTs,
		MsgQueryReference,
		MsgQueryRaw,
		MsgQueryRawScroll,
		MsgQueryExemplar,
		MsgQueryClusterMetrics,
		MsgRedisLock,
		MsgHandlerAPI,
		MsgTableFormat,
		MsgQueryRouter,
		MsgFeatureFlag,
		MsgHttpCurl,
	}

	for _, c := range constants {
		assert.NotEmpty(t, c, "constant should not be empty")
	}
}
