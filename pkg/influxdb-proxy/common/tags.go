// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package common

import (
	"bytes"
	"fmt"
	"hash/fnv"
	"strings"

	"github.com/influxdata/influxql"
)

var (
	DivideSymbol = "###"
	EqualSymbol  = "=="
)

// GenerateBackendRoute 根据算法生成backend列表，被cluster和transport共用
func GenerateBackendRoute(tagsKey string, backends []string, groupBatch int) []string {
	// 以某个hash(key)生成的index为起点，获取足够数量的机器
	resultBackends := make([]string, 0, groupBatch)
	for i := 0; i < groupBatch; i++ {
		index := (hash(tagsKey) + i) % len(backends)
		resultBackends = append(resultBackends, backends[index])
	}
	return resultBackends
}

func hash(s string) int {
	h := fnv.New32a()
	h.Write([]byte(s))
	return int(h.Sum32())
}

// GetTagsKey 根据tags获取key
func GetTagsKey(db, measurement string, tagNames []string, tags Tags) string {
	var buf bytes.Buffer
	buf.WriteString(db + "/" + measurement + "/")
	checkRepeat := make(map[string]bool)
	count := 0
	// 以tagNames为顺序，利用tags生成一个字符串作为key
	for _, tagName := range tagNames {
		for _, tag := range tags {
			if string(tag.Key) == tagName {
				// 特殊情况下，会有该维度的重复条件，所以这里进行了去重
				if _, ok := checkRepeat[tagName]; !ok {
					checkRepeat[tagName] = true
					if count != 0 {
						buf.WriteString(DivideSymbol)
					}
					count++
					buf.Write(tag.Key)
					buf.WriteString(EqualSymbol)
					buf.Write(tag.Value)
				}
			}
		}
	}
	return buf.String()
}

// AnaylizeTagsKey 将tagsKey数据解析回参数
func AnaylizeTagsKey(tagsKey string) (db string, measurement string, tags Tags) {
	divideList := strings.Split(tagsKey, "/")
	db = divideList[0]
	measurement = divideList[1]
	tagList := strings.Split(divideList[2], DivideSymbol)
	tags = make(Tags, 0, len(tagList))
	for _, item := range tagList {
		name := strings.Split(item, EqualSymbol)[0]
		value := strings.Split(item, EqualSymbol)[1]
		tag := Tag{
			Key:   []byte(name),
			Value: []byte(value),
		}
		tags = append(tags, tag)
	}
	return
}

// GetTags  解析请求的表达式，并将里面的所有维度及其值获取。此处考虑递归遍历表达式中所有内容
// 而且，只关注OP为EQ(等于号)的内容，并默认将左方认为是维度，右方为值
func GetTags(expr influxql.Expr) Tags {
	var result Tags
	var name *influxql.VarRef
	var value string
	var ok bool
loop:
	switch expr.(type) {
	case *influxql.ParenExpr:
		parenExpr := expr.(*influxql.ParenExpr)
		result = append(result, GetTags(parenExpr.Expr)...)
	case *influxql.BinaryExpr:
		binaryExpr := expr.(*influxql.BinaryExpr)
		// 如果不是等号的操作，则需要继续递归左方和右方的所有内容
		if binaryExpr.Op != influxql.EQ {
			result = append(result, GetTags(binaryExpr.LHS)...)
			result = append(result, GetTags(binaryExpr.RHS)...)
			break
		}

		// 否则，此时是等号的操作，需要考虑将左方放入到维度中
		if name, ok = binaryExpr.LHS.(*influxql.VarRef); !ok {
			// 如果装换失败了，表示这个表达式不是简单的 A=B，对于我们的维度解析没有任何意义，放过它好了
			break
		}

		switch tempExpr := binaryExpr.RHS.(type) {
		// 右方表达式只能是：整形、字符串或者数字，否则不认
		// 太复杂的，我们二期见
		case *influxql.IntegerLiteral:
			value = fmt.Sprintf("%d", tempExpr.Val)
		case *influxql.NumberLiteral:
			value = fmt.Sprintf("%f", tempExpr.Val)
		case *influxql.StringLiteral:
			value = tempExpr.Val
		default:
			break loop
		}

		result = append(result, Tag{
			Key:   []byte(name.Val),
			Value: []byte(value),
		})
	}

	return result
}

// GetDimensionTag 解析维度，获取tag列表
func GetDimensionTag(tagNames []string, dimensions string) (Tags, error) {
	dimensionList := strings.Split(dimensions, ",")
	var result Tags
	for _, dimension := range dimensionList {
		entry := strings.Split(dimension, "=")
		if len(entry) == 2 {
			tag := Tag{
				Key:   []byte(entry[0]),
				Value: []byte(entry[1]),
			}
			result = append(result, tag)
		}

	}
	return result, nil
}

// GetSelectTag ：
func GetSelectTag(tagNames []string, sql string) (Tags, error) {
	stmt, err := influxql.ParseStatement(sql)
	if err != nil {
		return nil, err
	}
	statement := stmt.(*influxql.SelectStatement)
	tags := GetTags(statement.Condition)
	if len(tags) == 0 {
		return nil, ErrTagNotFound
	}
	return tags, nil
}
