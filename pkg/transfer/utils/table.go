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
	"fmt"
	"io"
	"strings"

	"github.com/olekukonko/tablewriter"
)

type Table struct {
	head        []string
	lines       [][]string
	columnCount int
	foot        []string
	isByTab     bool
	caption     bool
	captionText []string
	out         io.Writer
	*tablewriter.Table
}

func (t *Table) SetHeader(keys []string) {
	if t.isByTab {
		if t.columnCount == 0 {
			t.columnCount = len(keys)
		} else if t.columnCount != len(keys) {
			panic("headCount is not same with column")
		}
		t.head = keys
	} else {
		t.Table.SetHeader(keys)
	}
}

func (t *Table) Append(row []string) {
	if t.isByTab {
		if t.columnCount == 0 {
			t.columnCount = len(row)
		} else if t.columnCount != len(row) {
			panic("columnCount is not same")
		}
		t.lines = append(t.lines, row)
	} else {
		t.Table.Append(row)
	}
}

func (t *Table) SetFooter(keys []string) {
	if t.isByTab {
		if t.columnCount == 0 {
			t.columnCount = len(keys)
		} else if t.columnCount != len(keys) {
			panic("footCount is not same with column")
		}
		t.foot = keys
	} else {
		t.Table.SetFooter(keys)
	}
}

func (t *Table) SetCaption(caption bool, captionText ...string) {
	if t.isByTab {
		t.caption = caption
		t.captionText = captionText
	} else {
		t.Table.SetCaption(caption, captionText...)
	}
}

func (t *Table) Render() {
	if t.isByTab {
		if len(t.head) != 0 {
			_, _ = fmt.Fprintln(t.out, strings.Join(t.head, "\t"))
		}
		for _, line := range t.lines {
			_, _ = fmt.Fprintln(t.out, strings.Join(line, "\t"))
		}
		if len(t.foot) != 0 {
			_, _ = fmt.Fprintln(t.out, strings.Join(t.foot, "\t"))
		}
		if t.caption {
			_, _ = fmt.Fprintln(t.out, strings.Join(t.captionText, "\t"))
		}
	} else {
		t.Table.Render()
	}
}

func NewTableUtil(writer io.Writer, isByTab bool) *Table {
	return &Table{
		isByTab: isByTab,
		out:     writer,
		Table:   tablewriter.NewWriter(writer),
	}
}
