// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package prometheus

import (
	"github.com/prometheus/prometheus/storage"
)

// lazySeriesSet
type lazySeriesSet struct {
	create func() (s storage.SeriesSet, ok bool)

	set storage.SeriesSet
}

// Next
func (c *lazySeriesSet) Next() bool {
	if c.set != nil {
		return c.set.Next()
	}

	var ok bool
	c.set, ok = c.create()
	return ok
}

// Err
func (c *lazySeriesSet) Err() error {
	if c.set != nil {
		return c.set.Err()
	}
	return nil
}

// At
func (c *lazySeriesSet) At() storage.Series {
	if c.set != nil {
		return c.set.At()
	}
	return nil
}

// Warnings
func (c *lazySeriesSet) Warnings() storage.Warnings {
	if c.set != nil {
		return c.set.Warnings()
	}
	return nil
}
