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

const (
	eventPattern = `{
  "$schema": "http://json-schema.org/draft-04/schema#",
  "type": "object",
  "properties": {
    "data_id": {
      "type": "integer",
	  "minimum": 1
    },
    "access_token": {
      "type": "string"
    },
    "data": {
      "type": "array",
      "items": [
        {
          "type": "object",
          "properties": {
            "event_name": {
              "type": "string",
			  "minLength": 1
            },
            "event": {
              "type": "object",
              "properties": {
                "content": {
                  "type": "string",
			      "minLength": 1
                }
              },
              "required": [
                "content"
              ]
            },
            "target": {
              "type": "string",
			  "minLength": 1
            },
            "dimension": {
              "type": "object"
            },
            "timestamp": {
              "type": "integer"
            }
          },
          "required": [
            "event_name",
            "event",
            "target",
            "dimension",
            "timestamp"
          ]
        }
      ]
    }
  },
  "required": [
    "data_id",
    "access_token",
    "data"
  ]
}`

	timeSeriesPattern = `{
  "$schema": "http://json-schema.org/draft-04/schema#",
  "type": "object",
  "properties": {
    "data_id": {
      "type": "integer",
	  "minimum": 1
    },
    "access_token": {
      "type": "string"
    },
    "data": {
      "type": "array",
      "items": [
        {
          "type": "object",
          "properties": {
            "metrics": {
              "type": "object",
              "properties": {}
            },
            "target": {
              "type": "string",
			  "minLength": 1
            },
            "dimension": {
              "type": "object",
              "properties": {}
            },
            "timestamp": {
              "type": "integer"
            }
          },
          "required": [
            "metrics",
            "target",
            "dimension",
            "timestamp"
          ]
        }
      ]
    }
  },
  "required": [
    "data_id",
    "access_token",
    "data"
  ]
}`
)

var (
	eventSchema      *gojsonschema.Schema
	timeSeriesSchema *gojsonschema.Schema
)

func init() {
	eventSchema = mustLoadSchema(eventPattern)
	timeSeriesSchema = mustLoadSchema(timeSeriesPattern)
}

func mustLoadSchema(s string) *gojsonschema.Schema {
	loader := gojsonschema.NewStringLoader(s)
	schema, err := gojsonschema.NewSchema(loader)
	if err != nil {
		panic(err)
	}

	return schema
}

func validateWithJSONSchema(schema *gojsonschema.Schema, content string) error {
	documentLoader := gojsonschema.NewStringLoader(content)
	result, err := schema.Validate(documentLoader)
	if err != nil {
		return fmt.Errorf("schema: failed to decode schema, err: %v", err.Error())
	}

	if result.Valid() {
		return nil
	}

	var errMsg string
	for _, err := range result.Errors() {
		if err != nil {
			errMsg += fmt.Sprintf("%s\n", err.Description())
		}
	}

	if len(errMsg) > 0 {
		return errors.New(errMsg)
	}

	return nil
}

func ValidateSchema(content string) bool {
	return ValidateEventSchema(content) == nil || ValidateTimeSeriesSchema(content) == nil
}

func ValidateEventSchema(content string) error {
	return validateWithJSONSchema(eventSchema, content)
}

func ValidateTimeSeriesSchema(content string) error {
	return validateWithJSONSchema(timeSeriesSchema, content)
}
