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
	"sync"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
)

type PayloadFlag uint8

const (
	PayloadFlagNoGroups     PayloadFlag = 1 << 0
	PayloadFlagNoCmdbLevels PayloadFlag = 1 << 1
)

// BasePayload :
type BasePayload struct {
	sn   int
	meta *sync.Map
	Data []byte
	t    time.Time
	flag PayloadFlag

	r *ETLRecord
}

func (p BasePayload) copy() *BasePayload {
	return &p
}

// SN :
func (p *BasePayload) SN() int {
	return p.sn
}

// Meta
func (p *BasePayload) Meta() PayloadMeta {
	if p.meta == nil {
		p.meta = &sync.Map{}
	}
	return p.meta
}

// Format :
func (p *BasePayload) Format(s fmt.State, verb rune) {
	var msg string
	switch verb {
	case 'v':
		if s.Flag('-') {
			msg = string(p.Data)
		} else if s.Flag('+') {
			msg = string(p.Data)
		} else if s.Flag('#') {
			msg = p.String()
		} else {
			msg = fmt.Sprintf("#%v", p.sn)
		}
	default:
		msg = fmt.Sprintf(string(verb), p)
	}
	_, err := io.WriteString(s, msg)
	if err != nil {
		panic(err)
	}
}

// String :
func (p *BasePayload) String() string {
	return fmt.Sprintf("%v-%s", p.sn, p.Data)
}

// To : parse json payload to interface
func (p *BasePayload) To(v interface{}) error {
	panic(ErrNotImplemented)
}

// From : dump interface to json
func (p *BasePayload) From(v interface{}) error {
	panic(ErrNotImplemented)
}

func (p *BasePayload) SetTime(t time.Time) {
	p.t = t
}

func (p *BasePayload) GetTime() time.Time {
	return p.t
}

func (p *BasePayload) AddFlag(f PayloadFlag) {
	p.flag = p.flag | f
}

func (p *BasePayload) SetFlag(f PayloadFlag) {
	p.flag = f
}

func (p *BasePayload) Flag() PayloadFlag {
	return p.flag
}

func (p *BasePayload) SetETLRecord(r *ETLRecord) {
	p.r = r
}

func (p *BasePayload) GetETLRecord() *ETLRecord {
	return p.r
}

// NewBasePayloadFrom :
func NewBasePayloadFrom(data []byte, sn int) *BasePayload {
	return &BasePayload{
		sn:   sn,
		Data: data,
	}
}

// JSONPayload :
type JSONPayload struct {
	*BasePayload
}

func (p JSONPayload) copy() Payload {
	p.BasePayload = p.BasePayload.copy()
	return &p
}

// Type
func (p *JSONPayload) Type() string {
	return "json"
}

// To : parse json payload to interface
func (p *JSONPayload) To(v interface{}) error {
	switch v.(type) {
	case *[]byte:
		*(v.(*[]byte)) = p.Data
		return nil
	default:
		return json.Unmarshal(p.Data, v)
	}
}

// From : dump interface to json
func (p *JSONPayload) From(v interface{}) error {
	var (
		js  []byte
		err error
	)
	switch value := v.(type) {
	case []byte:
		js = value
	default:
		js, err = json.MarshalFast(v)
	}
	p.Data = js
	return err
}

// DerivePayload
func DerivePayload(payload Payload, v interface{}) (derived Payload, err error) {
	switch t := payload.(type) {
	case payloadCopier:
		derived = t.copy()
	default:
		derived, err = NewPayload(t.Type(), t.SN())
		if err != nil {
			return nil, err
		}
		derived.SetTime(t.GetTime())
		derived.SetFlag(t.Flag())
	}

	err = derived.From(v)
	if err != nil {
		return nil, err
	}

	return derived, nil
}

// NewJSONPayload :
func NewJSONPayload(sn int) *JSONPayload {
	return NewJSONPayloadFrom(make([]byte, 0), sn)
}

// NewJSONPayloadFrom :
func NewJSONPayloadFrom(data []byte, sn int) *JSONPayload {
	return &JSONPayload{
		BasePayload: NewBasePayloadFrom(data, sn),
	}
}

func init() {
	RegisterPayload("json", func(name string, sn int) (Payload, error) {
		return NewJSONPayload(sn), nil
	})
}
