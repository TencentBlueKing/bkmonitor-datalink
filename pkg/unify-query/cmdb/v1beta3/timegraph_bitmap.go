// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package v1beta3

import "math/bits"

// timeBitmap keeps the normal <=64-point case allocation-free. Larger grids
// use additional words. Set operations never mutate either operand, so maps
// and attribute versions can safely share their backing words.
type timeBitmap struct {
	first uint64
	rest  []uint64
}

// set is only used while constructing a private bitmap, before it is shared.
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
