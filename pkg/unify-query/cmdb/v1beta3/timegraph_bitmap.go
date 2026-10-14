// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import "math/bits"

// timeBitmap 在不超过 64 帧时不分配额外存储；更多帧用后续字扩展。
// 集合运算不修改操作数，节点与属性版本可以安全共享底层位图字。
type timeBitmap struct {
	first uint64
	rest  []uint64
}

// set 仅在私有位图构建阶段调用，共享后不再修改。
func (b *timeBitmap) set(index int) {
	if index < 64 {
		b.first |= uint64(1) << index
		return
	}
	word := index/64 - 1
	if word >= len(b.rest) {
		b.rest = append(b.rest, make([]uint64, word-len(b.rest)+1)...)
	}
	b.rest[word] |= uint64(1) << (index % 64)
}

func (b timeBitmap) has(index int) bool {
	if index < 64 {
		return b.first&(uint64(1)<<index) != 0
	}
	word := index/64 - 1
	return word < len(b.rest) && b.rest[word]&(uint64(1)<<(index%64)) != 0
}

func (b timeBitmap) empty() bool {
	if b.first != 0 {
		return false
	}
	for _, word := range b.rest {
		if word != 0 {
			return false
		}
	}
	return true
}

func (b timeBitmap) count() int {
	count := bits.OnesCount64(b.first)
	for _, word := range b.rest {
		count += bits.OnesCount64(word)
	}
	return count
}

func (b timeBitmap) union(other timeBitmap) timeBitmap {
	result := timeBitmap{first: b.first | other.first}
	if size := max(len(b.rest), len(other.rest)); size > 0 {
		result.rest = make([]uint64, size)
		copy(result.rest, b.rest)
		for i, word := range other.rest {
			result.rest[i] |= word
		}
	}
	return result
}

func (b timeBitmap) intersect(other timeBitmap) timeBitmap {
	result := timeBitmap{first: b.first & other.first}
	if size := min(len(b.rest), len(other.rest)); size > 0 {
		result.rest = make([]uint64, size)
		for i := range result.rest {
			result.rest[i] = b.rest[i] & other.rest[i]
		}
	}
	return result
}

func (b timeBitmap) subtract(other timeBitmap) timeBitmap {
	result := timeBitmap{first: b.first &^ other.first}
	if len(b.rest) > 0 {
		result.rest = append([]uint64(nil), b.rest...)
		for i := 0; i < min(len(b.rest), len(other.rest)); i++ {
			result.rest[i] &^= other.rest[i]
		}
	}
	return result
}

// isSubsetOf 逐字检查包含关系，避免分配差集位图。
func (b timeBitmap) isSubsetOf(other timeBitmap) bool {
	if b.first&^other.first != 0 {
		return false
	}
	for index, word := range b.rest {
		if index >= len(other.rest) {
			if word != 0 {
				return false
			}
		} else if word&^other.rest[index] != 0 {
			return false
		}
	}
	return true
}

// intersectsBoth 不分配中间位图，直接检查三组位是否存在共同时间点。
func (b timeBitmap) intersectsBoth(left, right timeBitmap) bool {
	if b.first&left.first&right.first != 0 {
		return true
	}
	for index := 0; index < min(len(b.rest), len(left.rest), len(right.rest)); index++ {
		if b.rest[index]&left.rest[index]&right.rest[index] != 0 {
			return true
		}
	}
	return false
}

// trimTrailingZeroWords 去掉末尾的零字，恢复逐帧 set 构造出的表示形式。
// 只调整切片头，不修改共享的底层位图字。
func (b timeBitmap) trimTrailingZeroWords() timeBitmap {
	for len(b.rest) > 0 && b.rest[len(b.rest)-1] == 0 {
		b.rest = b.rest[:len(b.rest)-1]
	}
	return b
}
