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
	"github.com/xeipuuv/gojsonschema"
)

var eventSchemaStr = `{
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

var timeSeriesSchemaStr = `{
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

func loadSchema(s string) (*gojsonschema.Schema, error) {
	loader := gojsonschema.NewStringLoader(s)
	schema, err := gojsonschema.NewSchema(loader)
	if err != nil {
		return nil, err
	}
	return schema, nil
}

var (
	eventSchema      *gojsonschema.Schema
	timeseriesSchema *gojsonschema.Schema
)

func init() {
	var err error
	eventSchema, err = loadSchema(eventSchemaStr)
	if err != nil {
		panic(err)
	}

	timeseriesSchema, err = loadSchema(timeSeriesSchemaStr)
	if err != nil {
		panic(err)
	}
}
