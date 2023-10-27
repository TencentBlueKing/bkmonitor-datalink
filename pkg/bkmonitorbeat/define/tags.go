// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package define

// 标志位枚举
var (
	Upper CompareFlag = 1
	Lower CompareFlag = 2
	Equal CompareFlag = 3
)

// CompareFlag 比较结果标志位
type CompareFlag int

// Tag 从Labels里面获取到的传输tag键值对
type Tag struct {
	Key   string
	Value string
}

// Tags 全部Tag集合,实现了sort.Interface接口，可排序
type Tags []Tag

func (t Tags) Len() int {
	return len(t)
}

// Less key字符串长度长的排前面,长度相同则比较byte数组
func (t Tags) Less(i, j int) bool {
	tag1 := t[i]
	tag2 := t[j]
	// 对比key值
	key1 := tag1.Key
	key2 := tag2.Key
	switch t.CompareString(key1, key2) {
	case Upper:
		return true
	case Lower:
		return false
	}

	// 如果上面没有返回1,2，则证明key值相等，进行value对比
	value1 := tag1.Value
	value2 := tag1.Value
	switch t.CompareString(value1, value2) {
	case Upper:
		return true
	case Lower:
		return false
	}

	// 返回3证明相等,也返回false
	return false
}

func (t Tags) Swap(i, j int) {
	t[i], t[j] = t[j], t[i]
}

// CompareString 比较两个字符串大小 1.大于 2.小于 3.等于
func (t Tags) CompareString(a, b string) CompareFlag {
	length1 := len(a)
	length2 := len(b)
	// 先比较长度
	if length1 > length2 {
		return Upper
	}
	if length1 < length2 {
		return Lower
	}
	// 二者长度相等，则进一步比较
	byte1 := []byte(a)
	byte2 := []byte(b)
	for i := 0; i < length1; i++ {
		if byte1[i] > byte2[i] {
			return Upper
		}
		if byte1[i] < byte2[i] {
			return Lower
		}
	}

	return Equal
}
