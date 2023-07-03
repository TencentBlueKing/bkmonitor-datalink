// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

import (
	"fmt"
	"io"
	"strings"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/encoding/unicode/utf32"
)

// CharSetTransformer
type CharSetTransformer interface {
	Bytes(b []byte) ([]byte, error)
	String(s string) (string, error)
}

// CharSetDecoder
type CharSetDecoder interface {
	CharSetTransformer
	Reader(r io.Reader) io.Reader
}

// CharSetEncoder
type CharSetEncoder interface {
	CharSetTransformer
	Writer(w io.Writer) io.Writer
}

// DummyCharSetTransformer
type DummyCharSetTransformer struct{}

// Bytes
func (DummyCharSetTransformer) Bytes(b []byte) ([]byte, error) {
	return b, nil
}

// Bytes
func (DummyCharSetTransformer) String(s string) (string, error) {
	return s, nil
}

// Bytes
func (DummyCharSetTransformer) Reader(r io.Reader) io.Reader {
	return r
}

// Writer
func (DummyCharSetTransformer) Writer(w io.Writer) io.Writer {
	return w
}

// NewDummyCharSet
func NewDummyCharSetTransformer() DummyCharSetTransformer {
	return DummyCharSetTransformer{}
}

// NewDummyCharSetDecoder
func NewDummyCharSetDecoder(name string) (decoder CharSetDecoder, e error) {
	return NewDummyCharSetTransformer(), nil
}

// NewDummyCharSetEncoder
func NewDummyCharSetEncoder(name string) (encoder CharSetEncoder, e error) {
	return NewDummyCharSetTransformer(), nil
}

// RegisterStandardCharSet
func RegisterAutoNamedCharSetDecoder(name string, fn CharSetDecoderCreator) {
	RegisterCharSetDecoder(strings.ToUpper(name), fn)
	RegisterCharSetDecoder(strings.ToLower(name), fn)
}

// RegisterAutoNamedCharSetEncoder
func RegisterAutoNamedCharSetEncoder(name string, fn CharSetEncoderCreator) {
	RegisterCharSetEncoder(strings.ToUpper(name), fn)
	RegisterCharSetEncoder(strings.ToLower(name), fn)
}

// RegisterAutoNamedCharSetByEncoding
func RegisterAutoNamedCharSetByEncoding(name string, ec encoding.Encoding) {
	RegisterAutoNamedCharSetEncoder(name, func(name string) (CharSetEncoder, error) {
		return ec.NewEncoder(), nil
	})
	RegisterAutoNamedCharSetDecoder(name, func(name string) (CharSetDecoder, error) {
		return ec.NewDecoder(), nil
	})
}

// RegisterEncodings
func RegisterEncodings(encodings []encoding.Encoding) {
	replacer := strings.NewReplacer(
		" ", "-",
		"(", "",
		")", "",
	)
	for _, e := range encodings {
		RegisterAutoNamedCharSetByEncoding(replacer.Replace(fmt.Sprintf("%v", e)), e)
	}
}

func init() {
	RegisterAutoNamedCharSetEncoder("ascii", NewDummyCharSetEncoder)
	RegisterAutoNamedCharSetDecoder("ascii", NewDummyCharSetDecoder)
	RegisterAutoNamedCharSetEncoder("default", NewDummyCharSetEncoder)
	RegisterAutoNamedCharSetDecoder("default", NewDummyCharSetDecoder)

	RegisterEncodings(unicode.All)
	RegisterEncodings(utf32.All)
	RegisterEncodings(traditionalchinese.All)
	RegisterEncodings(simplifiedchinese.All)
	RegisterEncodings(charmap.All)
	RegisterEncodings(unicode.All)
}
