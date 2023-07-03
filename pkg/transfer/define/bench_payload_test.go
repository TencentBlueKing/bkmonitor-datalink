// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define_test

import (
	"bytes"
	"encoding/gob"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/bytedance/sonic"
	"github.com/jinzhu/copier"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
)

func benchmarkEncodeRecord(b *testing.B, encoder func(interface{}) error) {
	record := &define.ETLRecord{
		Time: new(int64),
		Dimensions: map[string]interface{}{
			"key": "x",
		},
		Metrics: map[string]interface{}{
			"value": 1.0,
		},
	}
	var err error

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err = encoder(record)
	}

	b.StopTimer()
	if err != nil {
		b.Fatal(err)
	}
}

func benchmarkEncodeMap(b *testing.B, encoder func(interface{}) error) {
	record := map[string]interface{}{
		"time": 0,
		"dimensions": map[string]interface{}{
			"key": "x",
		},
		"metrics": map[string]interface{}{
			"value": 1.0,
		},
	}
	var err error

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err = encoder(record)
	}

	b.StopTimer()
	if err != nil {
		b.Fatal(err)
	}
}

func benchmarkDecodeJSON(b *testing.B, decoder func([]byte) error) {
	js := []byte(`{"time":0,"dimensions":{"key":"x"},"metrics":{"value":1}}`)
	var err error

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err = decoder(js)
	}

	b.StopTimer()
	if err != nil {
		b.Fatal(err)
	}
}

func benchmarkDecodeGob(b *testing.B, decoder func([]byte) error) {
	g, err := hex.DecodeString("3dff810301010945544c5265636f726401ff82000103010454696d65010400010a44696d656e73696f6e7301ff840001074d65747269637301ff8400000027ff83040101176d61705b737472696e675d696e74657266616365207b7d01ff8400010c011000002bff820201036b657906737472696e670c0300017801010576616c756507666c6f61743634080400fef03f00")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err = decoder(g)
	}

	b.StopTimer()
	if err != nil {
		b.Fatal(err)
	}
}

// BenchmarkEncodeRecordJSON_Encoding
func BenchmarkEncodeRecordJSON_Encoding(b *testing.B) {
	benchmarkEncodeRecord(b, func(i interface{}) error {
		_, err := json.Marshal(i)
		return err
	})
}

// BenchmarkEncodeMapJSON_Encoding
func BenchmarkEncodeMapJSON_Encoding(b *testing.B) {
	benchmarkEncodeMap(b, func(i interface{}) error {
		_, err := json.Marshal(i)
		return err
	})
}

// BenchmarkDecodeJSONRecord_Encoding
func BenchmarkDecodeJSONRecord_Encoding(b *testing.B) {
	benchmarkDecodeJSON(b, func(bytes []byte) error {
		record := define.NewETLRecord()
		return json.Unmarshal(bytes, record)
	})
}

// BenchmarkDecodeJSONMap_Encoding
func BenchmarkDecodeJSONMap_Encoding(b *testing.B) {
	benchmarkDecodeJSON(b, func(bytes []byte) error {
		record := make(map[string]interface{})
		return json.Unmarshal(bytes, &record)
	})
}

// BenchmarkEncodeRecordJSON_JSONIter
func BenchmarkEncodeRecordJSON_JSONIter(b *testing.B) {
	benchmarkEncodeRecord(b, func(i interface{}) error {
		_, err := sonic.Marshal(i)
		return err
	})
}

// BenchmarkEncodeMapJSON_JSONIter
func BenchmarkEncodeMapJSON_JSONIter(b *testing.B) {
	benchmarkEncodeMap(b, func(i interface{}) error {
		_, err := json.Marshal(i)
		return err
	})
}

// BenchmarkDecodeJSONRecord_JSONIter
func BenchmarkDecodeJSONRecord_JSONIter(b *testing.B) {
	benchmarkDecodeJSON(b, func(bytes []byte) error {
		record := define.NewETLRecord()
		return sonic.Unmarshal(bytes, record)
	})
}

// BenchmarkDecodeJSONMap_JSONIter
func BenchmarkDecodeJSONMap_JSONIter(b *testing.B) {
	benchmarkDecodeJSON(b, func(bytes []byte) error {
		record := make(map[string]interface{})
		return sonic.Unmarshal(bytes, &record)
	})
}

// BenchmarkEncodeRecordGob
func BenchmarkEncodeRecordGob(b *testing.B) {
	benchmarkEncodeRecord(b, func(i interface{}) error {
		buffer := bytes.NewBuffer(nil)
		return gob.NewEncoder(buffer).Encode(i)
	})
}

// BenchmarkDecodeGobRecord
func BenchmarkDecodeGobRecord(b *testing.B) {
	benchmarkDecodeGob(b, func(b []byte) error {
		buffer := bytes.NewBuffer(b)
		record := define.NewETLRecord()
		return gob.NewDecoder(buffer).Decode(record)
	})
}

// BenchmarkCopyRecord
func BenchmarkCopyRecord(b *testing.B) {
	benchmarkEncodeRecord(b, func(i interface{}) error {
		record := define.ETLRecord{
			Time:       new(int64),
			Dimensions: make(map[string]interface{}),
			Metrics:    make(map[string]interface{}),
		}
		raw := i.(*define.ETLRecord)

		*record.Time = *raw.Time
		for key, value := range raw.Dimensions {
			record.Dimensions[key] = value
		}
		for key, value := range raw.Metrics {
			record.Metrics[key] = value
		}
		return nil
	})
}

// BenchmarkCopyMap
func BenchmarkCopyMap(b *testing.B) {
	benchmarkEncodeMap(b, func(i interface{}) error {
		record := map[string]interface{}{
			"time":       0,
			"dimensions": make(map[string]interface{}),
			"metrics":    make(map[string]interface{}),
		}
		raw := i.(map[string]interface{})

		record["time"] = raw["time"]
		for key, value := range raw["dimensions"].(map[string]interface{}) {
			record["dimensions"].(map[string]interface{})[key] = value
		}
		for key, value := range raw["metrics"].(map[string]interface{}) {
			record["metrics"].(map[string]interface{})[key] = value
		}
		return nil
	})
}

// BenchmarkDeepCopyRecord
func BenchmarkDeepCopyRecord(b *testing.B) {
	benchmarkEncodeRecord(b, func(i interface{}) error {
		record := define.NewETLRecord()
		return copier.Copy(record, i)
	})
}
