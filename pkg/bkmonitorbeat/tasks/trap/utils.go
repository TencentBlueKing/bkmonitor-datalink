// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package trap

import (
	"bytes"
	"io"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/transform"
)

// encodeFunc 编码转换函数字典
var encodeFunc = make(map[string]func(s []byte) ([]byte, error))

func init() {
	encodeFunc[""] = Nochange
	encodeFunc["gb2312"] = Gb2312ToUtf8
	encodeFunc["gbk"] = GbkToUtf8
	encodeFunc["gb18030"] = Gb18030ToUtf8
}

// Nochange 无转换
func Nochange(s []byte) ([]byte, error) {
	return s, nil
}

// Gb2312ToUtf8 gb2312->utf8
func Gb2312ToUtf8(s []byte) ([]byte, error) {
	return toUTF8(s, simplifiedchinese.HZGB2312.NewDecoder())
}

// GbkToUtf8 gbk->utf8
func GbkToUtf8(s []byte) ([]byte, error) {
	return toUTF8(s, simplifiedchinese.GBK.NewDecoder())
}

// Gb18030ToUtf8 gb18030->utf8
func Gb18030ToUtf8(s []byte) ([]byte, error) {
	return toUTF8(s, simplifiedchinese.GB18030.NewDecoder())
}

// toUTF8 按照编码转为utf8
func toUTF8(s []byte, decoder *encoding.Decoder) ([]byte, error) {
	reader := transform.NewReader(bytes.NewReader(s), decoder)
	d, e := io.ReadAll(reader)
	if e != nil {
		return nil, e
	}
	return d, nil
}
