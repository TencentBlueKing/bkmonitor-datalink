// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package parser

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/datadogrum/internal/model"
)

const validContext = `"application":{"id":"app"},"session":{"id":"session","type":"user"},"view":{"id":"view","url":"https://example.com"},"_dd":{"format_version":2}`

func TestParser_ParseViewUpdate(t *testing.T) {
	p := New(false)
	event, err := p.Parse([]byte(`{"type":"view_update","date":1,"application":{"id":"app"},"session":{"id":"session"},"view":{"id":"view","url":"https://example.com","performance":{"lcp":{"timestamp":376000000,"sub_parts":{"load_delay":306900000}}}},"_dd":{"format_version":2},"unknown_field":{"kept":true}}`))
	assert.NoError(t, err)
	assert.Equal(t, model.EventTypeViewUpdate, event.GetType())
	assert.Equal(t, int64(376000000), *event.(*model.ViewEvent).View.Performance.LCP.Timestamp)
}

func TestParser_ParseTimestampFallback(t *testing.T) {
	p := New(false)
	event, err := p.Parse([]byte(`{"type":"action","timestamp":42,"application":{"id":"app"},"session":{"id":"session"},"view":{"id":"view","url":"https://example.com"},"action":{"type":"click"}}`))
	assert.NoError(t, err)
	assert.Equal(t, int64(42), event.GetCommon().Date)
}

func TestParser_ParseBatch(t *testing.T) {
	p := New(false)
	rawEvents := [][]byte{
		[]byte(`{"type":"view","date":1,` + validContext + `,"view":{"id":"view","url":"https://example.com"}}`),
		[]byte(`{"type":"action","date":2,"application":{"id":"app"},"session":{"id":"session","type":"user"},"view":{"id":"view","url":"https://example.com"},"_dd":{"format_version":2},"action":{"id":"action","type":"click"}}`),
	}

	batch, err := p.ParseBatch(rawEvents)
	assert.NoError(t, err)
	assert.Len(t, batch.Events, 2)
	assert.Equal(t, model.EventTypeView, batch.Events[0].GetType())
}

func TestParser_Parse_Unsupported(t *testing.T) {
	p := New(false)
	_, err := p.Parse([]byte(`{"type":"unknown","date":1,"application":{"id":"app"}}`))
	assert.ErrorIs(t, err, model.ErrUnsupportedEventType)
}

func TestParser_Parse_MissingType(t *testing.T) {
	p := New(false)
	_, err := p.Parse([]byte(`{"date":1,"application":{"id":"app"}}`))
	assert.ErrorIs(t, err, model.ErrMissingRequiredField)
}

func TestParser_Parse_SkipInvalid(t *testing.T) {
	p := New(true)
	rawEvents := [][]byte{
		[]byte(`{"type":"unknown","date":1,"application":{"id":"app"}}`),
		[]byte(`{"type":"error","date":2,"application":{"id":"app"},"session":{"id":"session","type":"user"},"view":{"id":"view","url":"https://example.com"},"_dd":{"format_version":2},"error":{"id":"err","message":"failed","source":"source"}}`),
	}

	batch, err := p.ParseBatch(rawEvents)
	assert.NoError(t, err)
	assert.Len(t, batch.Events, 1)
	assert.Equal(t, model.EventTypeError, batch.Events[0].GetType())
}

func TestParser_Parse_Empty(t *testing.T) {
	p := New(false)
	_, err := p.ParseBatch(nil)
	assert.ErrorIs(t, err, model.ErrEmptyBody)
}

func TestParser_Parse_EmptyResourceObject(t *testing.T) {
	p := New(false)
	_, err := p.Parse([]byte(`{"type":"resource","date":1,"application":{"id":"app"},"session":{"id":"session","type":"user"},"view":{"id":"view","url":"https://example.com"},"_dd":{"format_version":2},"resource":{}}`))
	assert.NoError(t, err)
}

func TestParser_ParseNull(t *testing.T) {
	p := New(false)
	_, err := p.Parse([]byte("null"))
	assert.ErrorIs(t, err, model.ErrInvalidPayload)
}

func TestParser_ParseActionWithoutID(t *testing.T) {
	p := New(false)
	_, err := p.Parse([]byte(`{"type":"action","date":1,"application":{"id":"app"},"session":{"id":"session","type":"user"},"view":{"id":"view","url":"https://example.com"},"_dd":{"format_version":2},"action":{"type":"click"}}`))
	assert.NoError(t, err)
}
