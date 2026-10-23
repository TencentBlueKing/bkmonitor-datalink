// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License. See the License at http://opensource.org/licenses/MIT.

//go:build jsonsonic && ((amd64 && go1.17) || (arm64 && go1.20)) && !go1.26

package jsonx

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/stretchr/testify/require"
)

func TestSonicNativeJSONContract(t *testing.T) {
	require.Equal(t, sonic.UseSonicJSON, sonic.APIKind)
	input := map[string]any{
		"name": "监控 <node>&\"quoted\"", "count": float64(42), "empty": "", "null": nil,
		"labels": []any{"tenant-a", float64(-111)}, "headers": map[string]any{"Authorization": "test-token"},
	}
	encoders := map[string]func(any) ([]byte, error){
		"Marshal": Marshal,
		"MarshalString": func(v any) ([]byte, error) {
			data, err := MarshalString(v)
			return []byte(data), err
		},
		"MarshalIndent": func(v any) ([]byte, error) { return MarshalIndent(v, "", "  ") },
		"Encode": func(v any) ([]byte, error) {
			var buf bytes.Buffer
			err := Encode(&buf, v)
			return buf.Bytes(), err
		},
	}
	for name, encode := range encoders {
		t.Run(name, func(t *testing.T) {
			data, err := encode(input)
			require.NoError(t, err)
			require.NotContains(t, string(data), "<node>")
			require.Contains(t, string(data), `\u003cnode\u003e\u0026`)
			var got map[string]any
			require.NoError(t, json.Unmarshal(data, &got))
			require.Equal(t, input, got)
			_, err = encode(make(chan int))
			require.Error(t, err)
		})
	}
	data, err := json.Marshal(input)
	require.NoError(t, err)
	for name, decode := range map[string]func([]byte, any) error{
		"Unmarshal":       Unmarshal,
		"UnmarshalString": func(data []byte, v any) error { return UnmarshalString(string(data), v) },
		"Decode":          func(data []byte, v any) error { return Decode(bytes.NewReader(data), v) },
	} {
		t.Run(name, func(t *testing.T) {
			var got map[string]any
			require.NoError(t, decode(data, &got))
			require.Equal(t, input, got)
			require.Error(t, decode([]byte(`{"name":`), &got))
		})
	}
	// APM consumers retain decoded strings after the Kafka input buffer is reused.
	mutable := []byte(`{"name":"retained-string"}`)
	var retained struct {
		Name string `json:"name"`
	}
	require.NoError(t, Unmarshal(mutable, &retained))
	copy(mutable, strings.Repeat("x", len(mutable)))
	require.Equal(t, "retained-string", retained.Name)
	compact, err := Marshal(json.RawMessage("{\n \"ok\": true\n}"))
	require.NoError(t, err)
	require.Equal(t, `{"ok":true}`, string(compact))
}
