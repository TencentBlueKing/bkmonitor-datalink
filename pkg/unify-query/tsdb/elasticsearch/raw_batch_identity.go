// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package elasticsearch

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"sort"
	"strconv"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/metadata"
)

// RawBatchConnectionKey is comparable but intentionally does not expose or
// render the connection material from which it was derived.
type RawBatchConnectionKey struct {
	digest [sha256.Size]byte
}

// RawBatchConnectionKey returns the request-effective connection identity used
// only for in-process batch planning.
func (i *Instance) RawBatchConnectionKey(ctx context.Context) RawBatchConnectionKey {
	digester := sha256.New()
	writeRawBatchIdentityPart(digester, i.connect.Address)
	writeRawBatchIdentityPart(digester, i.connect.UserName)
	writeRawBatchIdentityPart(digester, i.connect.Password)
	writeRawBatchIdentityPart(digester, strconv.FormatInt(int64(i.timeout), 10))
	writeRawBatchIdentityPart(digester, strconv.FormatBool(i.healthCheck))

	headers := make(map[string]string, len(i.headers)+3)
	for name, value := range i.headers {
		headers[name] = value
	}
	headers = metadata.Headers(ctx, headers)
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		writeRawBatchIdentityPart(digester, name)
		writeRawBatchIdentityPart(digester, headers[name])
	}

	var digest [sha256.Size]byte
	copy(digest[:], digester.Sum(nil))
	return RawBatchConnectionKey{digest: digest}
}

func writeRawBatchIdentityPart(digester hash.Hash, value string) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = digester.Write(length[:])
	_, _ = digester.Write([]byte(value))
}
