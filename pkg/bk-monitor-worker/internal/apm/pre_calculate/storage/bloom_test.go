// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package storage

import (
	"crypto/md5"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/bk-monitor-worker/internal/apm/pre_calculate/core"
	"github.com/minio/highwayhash"
	"github.com/stretchr/testify/assert"
	boom "github.com/tylertreat/BoomFilters"
	"testing"
)

func TestExists(t *testing.T) {
	sbf := boom.NewScalableBloomFilter(10000, 0.01, 0.8)
	sbf.Add([]byte("00a03a4cce5618e6803d501a8b53f4d5"))
	assert.Equal(t, sbf.Test([]byte("5ab316c948a61737ac3005d8972bba5c")), false)
	assert.Equal(t, sbf.Test([]byte("0390577dbfbedd4a90c6f298b2fc99e9")), false)
	assert.Equal(t, sbf.Test([]byte("354ee86daa34778251c84ef6e506e9f1")), false)
	assert.Equal(t, sbf.Test([]byte("488761020445082f3bd255ee99ffa13e")), false)
	assert.Equal(t, sbf.Test([]byte("8fee8f742d8b4aed94ea2ffeff87e1b6")), false)
	assert.Equal(t, sbf.Test([]byte("b55ad0120589eb93716f5e3e3bd2244e")), false)
	assert.Equal(t, sbf.Test([]byte("b1daa202b36af1c325ca0b0f49e01990")), false)
	assert.Equal(t, sbf.Test([]byte("d8ccbd9187cc98d87de91e664b84e47a")), false)
	assert.Equal(t, sbf.Test([]byte("9edce68a6f5cb53c1c74502abf4579ad")), false)
	assert.Equal(t, sbf.Test([]byte("4ffef2b39c0461530f5d22008189ac0b")), false)
	assert.Equal(t, sbf.Test([]byte("8e29ecaa88d775d03ce6f2b3a263f74d")), false)
	assert.Equal(t, sbf.Test([]byte("2e14519dca83efcd791b361d85f2ed1f")), false)
}

func TestKeyHash(t *testing.T) {
	h, err := highwayhash.New([]byte(core.HashSecret))
	if err != nil {
		panic(err)
	}

	traceId := "b55ad0120589eb93716f5e3e3bd2244e"
	h.Write([]byte("b55ad0120589eb93716f5e3e3bd2244e"))
	key := h.Sum(nil)
	fmt.Printf("%s -> %d bytes", traceId, len(key))
}

func TestKeyMd5(t *testing.T) {
	traceId := "b55ad0120589eb93716f5e3e3bd2244e"

	hash := md5.New()
	hash.Write([]byte(traceId))
	shortStr := hex.EncodeToString(hash.Sum(nil))

	fmt.Println("Original string:", traceId)
	fmt.Println("Shortened string:", shortStr, "len", len(shortStr))
}

func TestKeyBase64(t *testing.T) {
	originalStr := "b55ad0120589eb93716f5e3e3bd2244e"

	encodedStr := base64.StdEncoding.EncodeToString([]byte(originalStr))

	fmt.Println("Original string:", originalStr)
	fmt.Println("Shortened string:", encodedStr, "len", len(encodedStr))
}

func TestNormalBloom(t *testing.T) {
	var blooms []boom.Filter

	sbf := boom.NewBloomFilter(uint(10000000000), 0.01)
	bloom1 := newBloomClient(sbf, func() { sbf.Reset() }, BloomOptions{})
	bloom2 := newBloomClient(sbf, func() { sbf.Reset() }, BloomOptions{})
	blooms = append(blooms, bloom1)
	blooms = append(blooms, bloom2)

	for index, b := range blooms {
		b.Add([]byte("b55ad0120589eb93716f5e3e3bd2244e"))
		fmt.Println(index, " exist -> ", b.Test([]byte("b55ad0120589eb93716f5e3e3bd2244e")))
	}
}
