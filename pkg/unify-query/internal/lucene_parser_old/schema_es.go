// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lucene_parser_old

import (
	"strings"
)

const (
	FieldTypeText    = "text"
	FieldTypeKeyword = "keyword"
	FieldTypeLong    = "long"
	FieldTypeInteger = "integer"
	FieldTypeFloat   = "float"
	FieldTypeDouble  = "double"
	FieldTypeDate    = "date"
	FieldTypeBoolean = "boolean"
	FieldTypeNested  = "nested"
)

const Empty = ""

const DefaultEmptyField = "log"

type esSchemaProvider interface {
	schemaProvider
	getNestedPath(fieldName string) (string, bool)
	getFieldName(field string) string
}

type ESSchema struct {
	*baseSchema
	mapping map[string]string
}

func NewESSchema(getFieldType func(field string) (string, bool), getAlias func(field string) string, mapping map[string]string) *ESSchema {
	return &ESSchema{
		baseSchema: NewBaseSchema(getFieldType, getAlias),
		mapping:    mapping,
	}
}

func (s *ESSchema) getNestedPath(fieldName string) (string, bool) {
	parts := strings.Split(fieldName, Separator)
	for i := len(parts) - 1; i >= 0; i-- {
		checkKey := strings.Join(parts[0:i], Separator)
		if fieldType, ok := s.getFieldType(checkKey); ok {
			if fieldType == FieldTypeNested {
				return checkKey, true
			}
		}
	}
	return "", false
}

func (s *ESSchema) getFieldName(fieldName string) string {
	if fieldName != Empty {
		return s.getAlias(fieldName)
	} else {
		return fieldName
	}
}
