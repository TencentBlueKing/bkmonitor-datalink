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
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/define"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
)

// CharsetSuite
type CharsetSuite struct {
	suite.Suite
}

func (s *CharsetSuite) testCharset(charset string, text string) {
	encoder, err := define.NewCharSetEncoder(charset)
	s.NoError(err, charset)

	decoder, err := define.NewCharSetDecoder(charset)
	s.NoError(err, charset)

	value, err := encoder.Bytes([]byte(text))
	s.NoError(err, charset)

	buffer := bytes.NewBuffer([]byte(`{"value": "`))
	buffer.Write(value)
	buffer.WriteString(`"}`)

	data := make(map[string]interface{})
	s.NoError(json.Unmarshal(buffer.Bytes(), &data), charset)

	result, err := decoder.String(data["value"].(string))
	s.NoError(err, charset)

	s.Equal(text, result, charset)
}

func (s *CharsetSuite) testCharsets(charsets []string, text string) {
	for _, c := range charsets {
		s.testCharset(c, text)
	}
}

// TestSimplifiedChinese
func (s *CharsetSuite) TestSimplifiedChinese() {
	s.testCharsets([]string{
		"GB18030", "GBK", "HZ-GB2312",
	}, "中国")
}

// TestTraditionalChinese
func (s *CharsetSuite) TestTraditionalChinese() {
	s.testCharsets([]string{
		"BIG5",
	}, "中國")
}

// TestCharMap
func (s *CharsetSuite) TestCharMap() {
	s.testCharsets([]string{
		"ISO-8859-1",
	}, "abc")
}

// TestMixEncoding
func (s *CharsetSuite) TestMixEncoding() {
	text := "中国"
	cases := []string{
		"GBK",
		"BIG5",
		"UTF-8",
	}
	buffer := bytes.NewBuffer([]byte(`{`))

	for _, charset := range cases {
		encoder, err := define.NewCharSetEncoder(charset)
		s.NoError(err, charset)

		value, err := encoder.Bytes([]byte(text))
		s.NoError(err, charset)
		buffer.WriteString(`"`)
		buffer.WriteString(charset)
		buffer.WriteString(`": "`)
		buffer.Write(value)
		buffer.WriteString(`", `)
	}
	buffer.WriteString(`"default": "`)
	buffer.WriteString(text)
	buffer.WriteString(`"}`)

	data := make(map[string]interface{})
	s.NoError(json.Unmarshal(buffer.Bytes(), &data))

	cases = append(cases, "default")
	for _, charset := range cases {
		decoder, err := define.NewCharSetDecoder(charset)
		s.NoError(err)

		result, err := decoder.String(data[charset].(string))
		s.NoError(err, charset)

		s.Equal(text, result, charset)
	}
}

// TestCharsetSuite
func TestCharsetSuite(t *testing.T) {
	suite.Run(t, new(CharsetSuite))
}
