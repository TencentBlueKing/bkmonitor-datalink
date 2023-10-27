// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package utils

import (
	"bytes"
	"crypto/sha1"
	"encoding/gob"
	"fmt"
	"hash/fnv"
)

// HashIt : hash an object
func HashIt(object interface{}) string {
	var (
		buf     bytes.Buffer
		encoder = gob.NewEncoder(&buf)
	)

	err := encoder.Encode(object)
	if err != nil {
		panic(err)
	}
	return fmt.Sprintf("%x", sha1.Sum(buf.Bytes()))
}

// HashItUint32
func HashItUint32(object interface{}) uint32 {
	hash := fnv.New32a()
	_, err := hash.Write([]byte(HashIt(object)))
	if err != nil {
		// always ok
		panic(err)
	}

	return hash.Sum32()
}

// HashItInt
func HashItInt(object interface{}) int {
	return int(HashItUint32(object))
}

// HashItUint64
func HashItUint64(object interface{}) uint64 {
	hash := fnv.New64a()
	_, err := hash.Write([]byte(HashIt(object)))
	if err != nil {
		// always ok
		panic(err)
	}

	return hash.Sum64()
}
