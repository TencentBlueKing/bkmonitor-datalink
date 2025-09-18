// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lucene_parser

import (
	"fmt"
	"strings"
)

const (
	DorisTypeInt       = "INT"
	DorisTypeTinyInt   = "TINYINT"
	DorisTypeSmallInt  = "SMALLINT"
	DorisTypeLargeInt  = "LARGEINT"
	DorisTypeBigInt    = "BIGINT"
	DorisTypeFloat     = "FLOAT"
	DorisTypeDouble    = "DOUBLE"
	DorisTypeDecimal   = "DECIMAL"
	DorisTypeDecimalV3 = "DECIMALV3"

	DorisTypeDate      = "DATE"
	DorisTypeDatetime  = "DATETIME"
	DorisTypeTimestamp = "TIMESTAMP"

	DorisTypeBoolean = "BOOLEAN"

	DorisTypeString     = "STRING"
	DorisTypeText       = "TEXT"
	DorisTypeVarchar512 = "VARCHAR(512)"

	DorisTypeArrayTransform = "%s ARRAY"
	DorisTypeArray          = "ARRAY<%s>"
)

type dorisSchemaProvider interface {
	schemaProvider
	isText(field string) bool
	transformField(field string) string
	formatValue(field, value string) string
}

type dorisSchema struct {
	*baseSchema
}

func NewDorisSchema(getFieldType func(field string) (string, bool), getAlias func(field string) string) *dorisSchema {
	// Wrap getFieldType to convert types to uppercase
	dorisGetFieldType := func(field string) (string, bool) {
		fieldType, exists := getFieldType(field)
		if !exists {
			return "", false
		}
		return strings.ToUpper(fieldType), true
	}

	return &dorisSchema{
		baseSchema: NewBaseSchema(dorisGetFieldType, getAlias),
	}
}

func (s *dorisSchema) isText(field string) bool {
	cleanField := strings.Trim(field, "`")

	if fieldType, ok := s.getFieldType(cleanField); ok && fieldType == DorisTypeText {
		return true
	}

	return false
}

// transformField 转换字段名，处理对象字段的CAST逻辑
func (s *dorisSchema) transformField(field string) string {
	if field == Empty || field == "*" {
		return field
	}

	cleanField := strings.Trim(field, "`")

	parts := strings.Split(cleanField, ".")
	if len(parts) == 1 {
		return "`" + cleanField + "`"
	}

	fieldType, exists := s.getFieldType(cleanField)
	if !exists {
		fieldType = DorisTypeString
	}

	// 构建CAST表达式：__ext.container_name -> CAST(__ext['container_name'] AS STRING)
	castExpression := parts[0] + "['" + strings.Join(parts[1:], ".") + "']"
	return "CAST(" + castExpression + " AS " + fieldType + ")"
}

// formatValue formats a value appropriately for Doris SQL based on field type
func (s *dorisSchema) formatValue(field, value string) string {
	cleanField := strings.Trim(field, "`")

	if fieldType, ok := s.getFieldType(cleanField); ok {
		switch fieldType {
		case DorisTypeInt, DorisTypeTinyInt, DorisTypeSmallInt, DorisTypeLargeInt, DorisTypeBigInt,
			DorisTypeFloat, DorisTypeDouble, DorisTypeDecimal, DorisTypeDecimalV3:
			return value
		}
	}

	return fmt.Sprintf("'%s'", escapeSQL(value))
}
