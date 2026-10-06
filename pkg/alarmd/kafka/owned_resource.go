// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package kafka

import "sync"

// ownedResource closes a client this package opened for itself exactly once,
// and reports the same error to every later caller. It lived with the consumer
// service until that service was retired; the producer sinks were already using
// it, so it moved here rather than being deleted with its old neighbours.
type ownedResource struct {
	once  sync.Once
	close func() error
	err   error
}

func (r *ownedResource) Close() error {
	if r == nil || r.close == nil {
		return nil
	}
	r.once.Do(func() { r.err = r.close() })
	return r.err
}
