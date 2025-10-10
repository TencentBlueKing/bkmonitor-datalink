// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package reflectx

import (
	"reflect"
	"strings"
	"time"
	"unsafe"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/utils/stringx"
)

// CopyFromMap 将配置数据从 map[string]interface{} 注入到目标结构体（指针）。
// 特性：
// 1) 字段名匹配支持多种命名风格：原字段名 / snake_case / camelCase / lowerCamel
// 2) 支持嵌套结构体与指针到结构体，自动递归注入
// 3) 匿名嵌入结构体优先按字段名等价命名查找子 map 注入，若无则回退为在当前 map 扁平注入
// 4) 对不可导出字段在必要时通过 unsafe 进行设置（见 SetUnexportedFieldValue）
// 注意：dst 必须为可寻址的结构体指针；configMap 通常来自 JSON/YAML 反序列化的弱类型 map
func CopyFromMap(dst interface{}, configMap map[string]interface{}) {
	if dst == nil || configMap == nil {
		return
	}

	dstValue := reflect.ValueOf(dst)
	if dstValue.Kind() != reflect.Ptr {
		return
	}

	dstValue = dstValue.Elem()
	if !dstValue.CanSet() {
		return
	}

	dstType := dstValue.Type()
	for i := 0; i < dstValue.NumField(); i++ {
		dstField := dstValue.Field(i)
		dstFieldType := dstType.Field(i)

		// 检查字段是否可设置
		canSet := dstField.CanSet()
		isUnexported := IsUnexportedField(dstField, dstFieldType)

		snakeName := stringx.CamelToSnake(dstFieldType.Name)
		camelName := stringx.SnakeToCamel(snakeName)
		lowerCamelName := strings.ToLower(dstFieldType.Name[:1]) + dstFieldType.Name[1:]

		// 尝试多种字段名匹配方式
		fieldNames := []string{
			dstFieldType.Name, // 直接字段名
			snakeName,         // 转换为 snake_case
			camelName,         // 转换为 camelCase
			lowerCamelName,    // 小写开头的驼峰格式
		}

		var configValue interface{}
		var found bool
		for _, fieldName := range fieldNames {
			if val, exists := configMap[fieldName]; exists {
				configValue = val
				found = true
				break
			}
		}

		if !found {
			continue
		}

		// 处理嵌套结构体
		if dstField.Kind() == reflect.Struct && configValue != nil {
			if nestedMap, ok := convertToStringInterfaceMap(configValue); ok {
				if canSet {
					CopyFromMap(dstField.Addr().Interface(), nestedMap)
				} else if isUnexported {
					// 对于不可导出的嵌套结构体，使用 unsafe
					fieldPtr := unsafe.Pointer(dstField.UnsafeAddr())
					fieldValue := reflect.NewAt(dstField.Type(), fieldPtr).Elem()
					CopyFromMap(fieldValue.Addr().Interface(), nestedMap)
				}
				continue
			}
		}

		// 处理指针到结构体（值为 map）
		if dstField.Kind() == reflect.Ptr && dstField.Type().Elem().Kind() == reflect.Struct && configValue != nil {
			if nestedMap, ok := convertToStringInterfaceMap(configValue); ok {
				if canSet {
					if dstField.IsNil() {
						dstField.Set(reflect.New(dstField.Type().Elem()))
					}
					CopyFromMap(dstField.Interface(), nestedMap)
				} else if isUnexported {
					fieldPtr := unsafe.Pointer(dstField.UnsafeAddr())
					fieldValue := reflect.NewAt(dstField.Type(), fieldPtr).Elem()
					if fieldValue.IsNil() {
						fieldValue.Set(reflect.New(fieldValue.Type().Elem()))
					}
					CopyFromMap(fieldValue.Interface(), nestedMap)
				}
				continue
			}
		}

		// 处理嵌入结构体（匿名字段）
		// 策略：
		// 1) 优先尝试从与嵌入字段名“等价命名”(原名/snake/camel/lowerCamel)对应的子 map 注入
		// 2) 若未提供专门子 map，则回退为在当前 configMap 中查找并注入（等价于扁平展开）
		if dstFieldType.Anonymous && dstField.Kind() == reflect.Struct {
			// 尝试找到与字段名匹配的子配置
			candidates := []string{dstFieldType.Name, snakeName, camelName, lowerCamelName}
			var sub any
			for _, k := range candidates {
				if v, ok := configMap[k]; ok {
					sub = v
					break
				}
			}
			if m, ok := convertToStringInterfaceMap(sub); ok {
				if canSet {
					CopyFromMap(dstField.Addr().Interface(), m)
				} else if isUnexported {
					fieldPtr := unsafe.Pointer(dstField.UnsafeAddr())
					fieldValue := reflect.NewAt(dstField.Type(), fieldPtr)
					CopyFromMap(fieldValue.Interface(), m)
				}
				continue
			}
			// 回退：在当前 map 中查找嵌入结构体字段
			if canSet {
				CopyFromMap(dstField.Addr().Interface(), configMap)
			} else if isUnexported {
				fieldPtr := unsafe.Pointer(dstField.UnsafeAddr())
				fieldValue := reflect.NewAt(dstField.Type(), fieldPtr)
				CopyFromMap(fieldValue.Interface(), configMap)
			}
			continue
		}

		// 设置字段值
		if canSet {
			SetFieldValue(dstField, configValue)
		} else if isUnexported {
			SetUnexportedFieldValue(dstField, dstFieldType, configValue)
		}
	}
}

// SetFieldValue 设置字段值并处理常见类型转换与最小归一化。
// 行为：
// 1) 切片：目标为 []T，来源为通用切片/数组时逐元素递归转换为 T
// 2) map：目标为 map[string]V，来源为 map[string]interface{} 时逐值递归转换为 V
// 3) time.Duration：支持整型/浮点数值（视为纳秒）到 Duration
// 4) 指针/非指针互转：在可行时进行取址或解引用
// 5) 其余遵循 reflect 的 ConvertibleTo 规则
func SetFieldValue(field reflect.Value, value interface{}) error {
	if !field.CanSet() {
		return nil
	}

	valueReflect := reflect.ValueOf(value)

	// 归一化：当目标是切片且来源为通用切片/数组
	if field.Kind() == reflect.Slice {
		if valueReflect.Kind() == reflect.Slice || valueReflect.Kind() == reflect.Array {
			length := valueReflect.Len()
			newSlice := reflect.MakeSlice(field.Type(), 0, length)
			for i := 0; i < length; i++ {
				srcElem := valueReflect.Index(i).Interface()
				dstElem := reflect.New(field.Type().Elem()).Elem()
				_ = SetFieldValue(dstElem, srcElem)
				newSlice = reflect.Append(newSlice, dstElem)
			}
			field.Set(newSlice)
			return nil
		}
	}

	// 归一化：当目标是 map[string]T 且来源为 map[string]interface{}
	if field.Kind() == reflect.Map && field.Type().Key().Kind() == reflect.String {
		if valueReflect.Kind() == reflect.Map && valueReflect.Type().Key().Kind() == reflect.String {
			newMap := reflect.MakeMap(field.Type())
			for _, key := range valueReflect.MapKeys() {
				v := valueReflect.MapIndex(key).Interface()
				dstElem := reflect.New(field.Type().Elem()).Elem()
				_ = SetFieldValue(dstElem, v)
				newMap.SetMapIndex(key.Convert(field.Type().Key()), dstElem)
			}
			field.Set(newMap)
			return nil
		}
	}

	// 如果类型完全匹配
	if field.Type() == valueReflect.Type() {
		field.Set(valueReflect)
		return nil
	}

	// 处理指针类型转换
	if field.Type().Kind() == reflect.Ptr && valueReflect.Type().Kind() != reflect.Ptr {
		if valueReflect.CanAddr() {
			field.Set(valueReflect.Addr())
		} else {
			// 创建新值并取地址
			newValue := reflect.New(valueReflect.Type())
			newValue.Elem().Set(valueReflect)
			field.Set(newValue)
		}
		return nil
	}

	// 处理非指针类型转换
	if field.Type().Kind() != reflect.Ptr && valueReflect.Type().Kind() == reflect.Ptr {
		if !valueReflect.IsNil() {
			field.Set(valueReflect.Elem())
		}
		return nil
	}

	// 特殊处理 time.Duration 类型（数值按纳秒）
	if field.Type() == reflect.TypeOf(time.Duration(0)) {
		switch valueReflect.Kind() {
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			duration := time.Duration(valueReflect.Convert(reflect.TypeOf(int64(0))).Int())
			field.Set(reflect.ValueOf(duration))
			return nil
		case reflect.Float32, reflect.Float64:
			duration := time.Duration(int64(valueReflect.Convert(reflect.TypeOf(float64(0))).Float()))
			field.Set(reflect.ValueOf(duration))
			return nil
		}
	}

	// 处理基本类型转换（同 kind 但不同具体类型，如自定义类型基于基础类型）
	if field.Type().Kind() == valueReflect.Type().Kind() {
		// 优先尝试可转换
		if valueReflect.Type().ConvertibleTo(field.Type()) {
			field.Set(valueReflect.Convert(field.Type()))
			return nil
		}
		// 回退直接设置（完全相同类型时）
		if field.Type() == valueReflect.Type() {
			field.Set(valueReflect)
			return nil
		}
	}

	// 尝试类型转换
	if valueReflect.Type().ConvertibleTo(field.Type()) {
		field.Set(valueReflect.Convert(field.Type()))
		return nil
	}

	// 处理自定义类型转换（如 string 到基于 string 的自定义类型）
	if valueReflect.Kind() == reflect.String && field.Kind() == reflect.String {
		// 两端都是 string kind，统一按目标类型转换
		if reflect.TypeOf("").ConvertibleTo(field.Type()) {
			field.Set(reflect.ValueOf(valueReflect.String()).Convert(field.Type()))
			return nil
		}
	}

	return nil
}

// IsUnexportedField 判断字段是否为不可导出字段。
func IsUnexportedField(field reflect.Value, fieldType reflect.StructField) bool {
	// 检查字段是否真的不可导出
	if fieldType.PkgPath != "" {
		// 这是一个不可导出的字段，我们可以使用 unsafe 来访问它
		return true
	}

	// 对于嵌入结构体，检查结构体类型是否不可导出
	if fieldType.Anonymous && field.Kind() == reflect.Struct {
		// 检查结构体类型是否不可导出（类型名以小写字母开头）
		typeName := fieldType.Type.Name()
		if typeName != "" && typeName[0] >= 'a' && typeName[0] <= 'z' {
			return true
		}
	}

	return false
}

// SetUnexportedFieldValue 使用 unsafe 设置不可导出字段值。
// 若字段可导出则委托给 SetFieldValue；若不可导出，则通过 UnsafeAddr 获取地址并写入。
func SetUnexportedFieldValue(field reflect.Value, fieldType reflect.StructField, value interface{}) error {
	if !IsUnexportedField(field, fieldType) {
		// 字段是可导出的，使用普通方法
		return SetFieldValue(field, value)
	}

	// 字段不可导出，使用 unsafe
	fieldPtr := unsafe.Pointer(field.UnsafeAddr())
	fieldValue := reflect.NewAt(field.Type(), fieldPtr).Elem()

	return SetFieldValue(fieldValue, value)
}

// convertToStringInterfaceMap 将任意 key 类型为 string 的 map 转为 map[string]interface{}。
// 适用：当来源为 map[string]T（T 任意）而需要以通用 map 形态进行递归注入
// 返回：转换后的通用 map 及是否成功
func convertToStringInterfaceMap(v interface{}) (map[string]interface{}, bool) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() || rv.Kind() != reflect.Map || rv.Type().Key().Kind() != reflect.String {
		return nil, false
	}
	res := make(map[string]interface{}, rv.Len())
	for _, k := range rv.MapKeys() {
		res[k.String()] = rv.MapIndex(k).Interface()
	}
	return res, true
}
