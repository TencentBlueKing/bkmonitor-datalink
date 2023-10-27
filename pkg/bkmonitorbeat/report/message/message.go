// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package message

import (
	"errors"
	"fmt"

	"github.com/xeipuuv/gojsonschema"
)

// Message represent a message that will be send
type Message struct {
	Kind    string
	Content string
}

func (msg Message) ToBytes() []byte {
	return []byte(msg.Content)
}

// Validate do validate message content
func (msg Message) Validate() error {
	switch msg.Kind {
	case "event":
		return ValidateWithJSONSchema(eventSchema, msg.Content)
	case "timeseries":
		return ValidateWithJSONSchema(timeseriesSchema, msg.Content)
	default:
		return fmt.Errorf("unexpected message kind: %s", msg.Kind)
	}
}

func ValidateWithJSONSchema(schema *gojsonschema.Schema, content string) error {
	documentLoader := gojsonschema.NewStringLoader(content)
	result, err := schema.Validate(documentLoader)
	if err != nil {
		return fmt.Errorf("json schema validate failed, err: %+v", err.Error())
	}
	if result.Valid() {
		return nil
	}
	finalErr := ""
	for _, err := range result.Errors() {
		if err != nil {
			finalErr += fmt.Sprintf("%s\n", err.Description())
		}
	}
	if len(finalErr) > 0 {
		return errors.New(finalErr)
	}
	return nil
}
