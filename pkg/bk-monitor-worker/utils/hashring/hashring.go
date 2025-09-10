// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package hashring

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"math/big"
	"sort"
)

type HashRing[T comparable] struct {
	nodes     map[T]int
	ring      []uint64
	hash2node map[uint64]T
	numVnodes int
}

func NewHashRing[T comparable](nodes map[T]int, numVnodes int) *HashRing[T] {
	if numVnodes == 0 {
		numVnodes = 1 << 16
	}
	hr := &HashRing[T]{
		nodes:     nodes,
		numVnodes: numVnodes,
		ring:      make([]uint64, 0),
		hash2node: make(map[uint64]T),
	}

	var sumWeight int
	for _, weight := range nodes {
		sumWeight += weight
	}

	multiple := 1
	if numVnodes/sumWeight > 1 {
		multiple = numVnodes / sumWeight
	}

	hr.numVnodes = multiple * sumWeight

	for node := range nodes {
		for i := 0; i < multiple; i++ {
			h := hr.hash(fmt.Sprintf("%v%d", node, i))
			hr.ring = append(hr.ring, h)
			hr.hash2node[h] = node
		}
	}

	sort.Slice(hr.ring, func(i, j int) bool { return hr.ring[i] < hr.ring[j] })

	return hr
}

func (hr *HashRing[T]) hash(key string) uint64 {
	hasher := md5.New()
	hasher.Write([]byte(key))
	hashBytes := hasher.Sum(nil)
	// 将 MD5 哈希结果转换为十六进制字符串
	hashIntStr := hex.EncodeToString(hashBytes)
	// 将十六进制字符串转换为 uint64 类型
	d, _ := new(big.Int).SetString(hashIntStr, 16)
	// 对结果取模 2^32
	modulo := new(big.Int).SetUint64(1 << 32)
	return new(big.Int).Mod(d, modulo).Uint64()
}

func (hr *HashRing[T]) GetNode(key string) T {
	h := hr.hash(key)
	n := bisectLeft(hr.ring, h) % hr.numVnodes
	return hr.hash2node[hr.ring[n]]
}

func bisectLeft(a []uint64, x uint64) int {
	return sort.Search(len(a), func(i int) bool { return a[i] >= x })
}
