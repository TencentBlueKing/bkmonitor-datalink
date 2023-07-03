// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"bytes"
	"io"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/transfer/json"
)

// Records
type Records []*Record

// ASBody
func (r Records) AsBody() (io.Reader, error) {
	buffer := bytes.NewBuffer(nil)
	encoder := json.NewEncoder(buffer)
	for _, record := range r {
		err := encoder.Encode(map[string]interface{}{
			"index": record.Meta,
		})
		if err != nil {
			return nil, err
		}

		err = encoder.Encode(record.Document)
		if err != nil {
			return nil, err
		}
	}
	return buffer, nil
}

// ESRecord
type Record struct {
	Meta     map[string]interface{}
	Document interface{}
}

// NewRecord
func NewRecord(document interface{}) *Record {
	return &Record{
		Meta:     make(map[string]interface{}),
		Document: document,
	}
}

// SetID
func (r *Record) SetID(id string) {
	r.Meta["_id"] = id
}

// GetID
func (r *Record) GetID() string {
	v, ok := r.Meta["_id"]
	if !ok {
		return ""
	}
	id, ok := v.(string)
	if !ok {
		return ""
	}
	return id
}

// SetType
func (r *Record) SetType(name string) {
	r.Meta["_type"] = name
}

// String
func (r *Record) String() string {
	return r.GetID()
}
