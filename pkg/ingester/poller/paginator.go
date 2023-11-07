// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package poller

import (
	"strconv"
)

type Paginator interface {
	SetTotalToMax()
	SetTotal(int)
	GetAndNext() Context
	Reset()
	HasNext() bool
}

type BasePaginator struct {
	MaxSize  int
	PageSize int
	total    int
}

func (p *BasePaginator) SetTotalToMax() {
	p.total = p.MaxSize
}

func (p *BasePaginator) SetTotal(total int) {
	p.total = total
}

func (p *BasePaginator) Reset() {
	p.total = 0
}

type PageNumberPaginator struct {
	BasePaginator
	currentPage int
}

func (p *PageNumberPaginator) GetAndNext() Context {
	context := Context{
		// 一般来说，页数都是从1开始的
		"page":      strconv.Itoa(p.currentPage + 1),
		"page_size": strconv.Itoa(p.PageSize),
	}
	p.currentPage += 1
	return context
}

func (p *PageNumberPaginator) Reset() {
	p.currentPage = 0
	p.BasePaginator.Reset()
}

func (p *PageNumberPaginator) HasNext() bool {
	currentCount := p.currentPage * p.PageSize
	if p.MaxSize > 0 {
		return currentCount < p.total && currentCount < p.MaxSize
	}
	return currentCount < p.total
}

type LimitOffsetPaginator struct {
	BasePaginator
	currentOffset int
}

func (p *LimitOffsetPaginator) GetAndNext() Context {
	context := Context{
		"offset": strconv.Itoa(p.currentOffset),
		"limit":  strconv.Itoa(p.PageSize),
	}
	p.currentOffset += p.PageSize
	return context
}

func (p *LimitOffsetPaginator) Reset() {
	p.currentOffset = 0
	p.BasePaginator.Reset()
}

func (p *LimitOffsetPaginator) HasNext() bool {
	if p.MaxSize > 0 {
		return p.currentOffset < p.total && p.currentOffset < p.MaxSize
	}
	return p.currentOffset < p.total
}

type NilPaginator struct {
	BasePaginator
}

func (p *NilPaginator) GetAndNext() Context {
	context := Context{}
	return context
}

func (p *NilPaginator) Reset() {
	p.BasePaginator.Reset()
}

func (p *NilPaginator) HasNext() bool {
	return false
}
