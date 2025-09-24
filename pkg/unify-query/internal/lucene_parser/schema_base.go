// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lucene_parser

// schemaProvider SchemaProvider在doris和es下面都有共同的需求,都需要提供字段类型和别名的获取
type schemaProvider interface {
	getAlias(field string) string
	getFieldType(field string) (string, bool)
}

type baseSchema struct {
	// 这里使用函数的方式是在屏蔽外面的schema细节,只提供两个方法分别用来直接回调外面的内容.
	getFieldTypeFunc func(field string) (string, bool)
	getAliasFunc     func(field string) string
}

func NewBaseSchema(getFieldType func(field string) (string, bool), getAlias func(field string) string) *baseSchema {
	return &baseSchema{
		getFieldTypeFunc: getFieldType,
		getAliasFunc:     getAlias,
	}
}

func (s *baseSchema) getFieldType(field string) (string, bool) {
	if s.getFieldTypeFunc != nil {
		return s.getFieldTypeFunc(field)
	}
	return "", false
}

func (s *baseSchema) getAlias(field string) string {
	if s.getAliasFunc != nil {
		return s.getAliasFunc(field)
	}
	return field
}
