// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package metadata

import (
	"fmt"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

// Expected values are written out from the __bk_<code point>__ rule, not produced by the decoder.
func TestFieldFormatDecodeCases(t *testing.T) {
	decode := new(FieldFormat).DecodeFunc()
	for _, c := range []struct{ name, in, want string }{
		{"plain", "container", "container"},
		{"empty", "", ""},
		{"one mark", "__ext__bk_46__container", "__ext.container"},
		{"repeated mark", "a__bk_46__b__bk_46__c", "a.b.c"},
		{"different marks", "__bk_33___ext__bk_47__x", "!_ext/x"},
		{"leading zeros", "a__bk_046__b", "a.b"},
		{"unicode", "__bk_20013____bk_25991__", "中文"},
		{"astral code point", "__bk_66376__", "\U00010348"},
		{"beyond unicode", "__bk_1114112__", "�"},
		{"atoi overflow is kept", "__bk_99999999999999999999__x", "__bk_99999999999999999999__x"},
		{"overflow then valid", "__bk_99999999999999999999____bk_46__", "__bk_99999999999999999999__."},
		{"letters inside", "__bk_x__", "__bk_x__"},
		{"no digits", "__bk___", "__bk___"},
		{"sign", "__bk_-46__", "__bk_-46__"},
		{"missing tail", "__bk_46", "__bk_46"},
		{"missing head", "bk_46__", "bk_46__"},
		{"single underscores", "_bk_46_", "_bk_46_"},
		{"no rescan after replace", "__bk_95__bk_46__", "_bk_46__"},
	} {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, decode(c.in))
		})
	}
}

func TestFieldFormatEncodeDecodeRoundTrip(t *testing.T) {
	f := new(FieldFormat)
	encode, decode := f.EncodeFunc(), f.DecodeFunc()
	for _, q := range []string{
		"",
		"plain_name:1",
		"__ext.container",
		"__ext.cloud.tencent.com/asset-code",
		"!_ext...",
		"a b\tc",
		"中文.字段",
		"\U00010348-x",
	} {
		encoded := encode(q)
		assert.Equal(t, q, decode(encoded), encoded)
	}
}

func TestFieldFormatDecodeConcurrent(t *testing.T) {
	shared := new(FieldFormat).DecodeFunc()
	inputs := []struct{ in, want string }{
		{"__ext__bk_46__container", "__ext.container"},
		{"__bk_20013__", "中"},
		{"plain", "plain"},
		{"__bk_99999999999999999999__", "__bk_99999999999999999999__"},
	}
	var wg sync.WaitGroup
	errs := make(chan string, 64*len(inputs))
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Half the callers share one closure, half build their own.
			decode := shared
			if i%2 == 1 {
				decode = new(FieldFormat).DecodeFunc()
			}
			for _, c := range inputs {
				if got := decode(c.in); got != c.want {
					errs <- fmt.Sprintf("decode(%q) = %q, want %q", c.in, got, c.want)
				}
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

var decodeSink string

func BenchmarkFieldFormatDecode(b *testing.B) {
	decode := new(FieldFormat).DecodeFunc()
	for _, c := range []struct{ name, in string }{
		{"plain", "container"},
		{"encoded", "__ext__bk_46__cloud__bk_46__tencent__bk_46__com__bk_47__asset__bk_45__code"},
	} {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				decodeSink = decode(c.in)
			}
		})
	}
}
