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
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// InfluxDB reports a failure of the whole request in the top-level error, beside or instead of results;
// the JSON decoder keeps the first one so callers do not read it as an empty result.
func TestJSONKeepsTheTopLevelError(t *testing.T) {
	dec, err := GetDecoder("application/json")
	require.NoError(t, err)
	resp := new(Response)
	_, err = dec.Decode(context.Background(), strings.NewReader(
		`{"error":"error parsing query: found EOF"}`+"\n"+`{"error":"a later chunk"}`+"\n"), resp)
	require.NoError(t, err)
	require.Equal(t, "error parsing query: found EOF", resp.Err)
	require.Empty(t, resp.Results)

	resp = new(Response)
	_, err = dec.Decode(context.Background(), strings.NewReader(`{"results":[{"statement_id":0}]}`+"\n"), resp)
	require.NoError(t, err)
	require.Empty(t, resp.Err)
	require.Len(t, resp.Results, 1)
}

// endAfterFirstRead ends the context on its first read and then hands over the whole body: the decoder
// meets the end between two chunks it already holds.
type endAfterFirstRead struct {
	data  *strings.Reader
	end   func()
	ended bool
}

func (r *endAfterFirstRead) Read(p []byte) (int, error) {
	if !r.ended {
		r.ended = true
		r.end()
	}
	return r.data.Read(p)
}

// A decoder that stops because the request's context ended returns that context's error, so the stop is
// classified as a timeout or a cancel and not as a failed backend answer.
func TestDecodersKeepTheContextError(t *testing.T) {
	chunk, err := (&Response{Results: []Result{{StatementID: 0}}}).MarshalMsg(nil)
	require.NoError(t, err)
	bodies := map[string]string{
		"application/json":      `{"results":[{"statement_id":0}]}` + "\n" + `{"results":[{"statement_id":0}]}` + "\n",
		"application/x-msgpack": string(chunk) + string(chunk),
	}
	for contentType, body := range bodies {
		for name, want := range map[string]error{"deadline": context.DeadlineExceeded, "canceled": context.Canceled} {
			t.Run(contentType+"/"+name, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
				defer cancel()
				end := func() { <-ctx.Done() }
				if want == context.Canceled {
					end = cancel
				}
				dec, err := GetDecoder(contentType)
				require.NoError(t, err)
				_, err = dec.Decode(ctx, &endAfterFirstRead{data: strings.NewReader(body), end: end}, new(Response))
				require.ErrorIs(t, err, want)
			})
		}
	}
}
