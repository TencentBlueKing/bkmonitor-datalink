// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

//go:build aix || dragonfly || freebsd || linux || netbsd || openbsd || solaris || zos

package corefile

import (
	"os"
	"path"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/libgse/beat"
)

func TestTranslate(t *testing.T) {
	testCases := []struct {
		text     string
		expected string
	}{
		{"", ""},
		{"1", "hangup"},
		{"2", "interrupt"},
		{"9999", ""},
		{"abc", "abc"},
	}

	translator := &SignalTranslator{}

	for _, testCase := range testCases {
		result := translator.Translate(testCase.text)
		if result != testCase.expected {
			t.Errorf("Expected %s, but got %s", testCase.expected, result)
		}
	}
}

func TestExecutablePathTranslator_Translate(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"../path!to!file.ext", "../path/to/file.ext"},
		{"folder!/file!/name.ext", "folder/file/name.ext"},
		{"root!!file.txt", "root//file.txt"},
		{"no!exclamation!mark!in!path", "no!exclamation!mark!in!path"},
		{"!at!the!beginning", "/at/the/beginning"},
		{"at!the!end!", "at/the/end/"},
	}

	translator := &ExecutablePathTranslator{}

	for _, test := range tests {
		result := translator.Translate(test.path)
		if result != test.expected {
			t.Errorf("Translate(%q) = %q, want %q", test.path, result, test.expected)
		}
	}
}

type PatternTestCase struct {
	pattern  string
	error    error
	expected [][]string
}

func TestCheckPattern(t *testing.T) {
	testCases := []PatternTestCase{
		{
			pattern: `corefile-%E-%e-%I-%i-%s-%t`,
			error:   nil,
			expected: [][]string{
				{"corefile-%E", "corefile-", "%E"},
				{"-%e", "-", "%e"},
				{"-%I", "-", "%I"},
				{"-%i", "-", "%i"},
				{"-%s", "-", "%s"},
				{"-%t", "-", "%t"},
				{"%z", "", "%z"},
			},
		},
		{
			pattern: `%E-%e-%I-%i-%s-%t`,
			error:   nil,
			expected: [][]string{
				{"%E", "", "%E"},
				{"-%e", "-", "%e"},
				{"-%I", "-", "%I"},
				{"-%i", "-", "%i"},
				{"-%s", "-", "%s"},
				{"-%t", "-", "%t"},
				{"%z", "", "%z"},
			},
		},
		{
			pattern: `corefile%E-_%e-%I-%i-%s-%t_end`,
			error:   nil,
			expected: [][]string{
				{"corefile%E", "corefile", "%E"},
				{"-_%e", "-_", "%e"},
				{"-%I", "-", "%I"},
				{"-%i", "-", "%i"},
				{"-%s", "-", "%s"},
				{"-%t", "-", "%t"},
				{"_end%z", "_end", "%z"},
			},
		},
		{
			// 占位符之间没有分隔符
			pattern:  `%E%e-%I-%i-%s-%t`,
			error:    ErrPatternDelimiter,
			expected: nil,
		},
	}
	for _, item := range testCases {
		t.Logf("pattern: %s", item.pattern)
		c := new(Collector)
		c.pattern = item.pattern
		err := c.checkPattern()
		assert.Equal(t, item.error, err)
		if err != nil {
			continue
		}
		for i, e := range item.expected {
			assert.Equal(t, e[0], c.patternList[i][0])
			assert.Equal(t, e[1], c.patternList[i][1])
			assert.Equal(t, e[2], c.patternList[i][2])
		}
	}
}

type DimensionTestCase struct {
	pattern           string
	filePath          string
	isUsesPid         bool
	expected          map[string]string
	isAnalysisSuccess bool
}

func TestBuildDimensionKey(t *testing.T) {
	var testData = []struct {
		dimensions beat.MapStr
		result     string
	}{
		{
			dimensions: beat.MapStr{
				ExecutablePathKeyName: "/data/bin",
				ExecutableKeyName:     "bin",
				SignalKeyName:         "SIGKILL",
			},
			result: "/data/bin-bin-SIGKILL",
		},
		{
			dimensions: beat.MapStr{
				ExecutableKeyName: "bin",
				SignalKeyName:     "SIGKILL",
			},
			result: "-bin-SIGKILL",
		},
		{
			dimensions: beat.MapStr{},
			result:     "--",
		},
	}

	for _, data := range testData {
		result := buildDimensionKey(data.dimensions)
		assert.Equal(t, data.result, result)
	}
}

var (
	corePatternPathV2 = path.Join(os.TempDir(), "corefile_pattern")
)

func TestCoreFileCollectorGetCoreFilePath(t *testing.T) {
	CorePatternFile = corePatternPathV2
	err := os.WriteFile(corePatternPathV2, []byte("/data/corefile/core_%e_%t.%p\n"), 0644)
	if err != nil {
		panic(err)
	}

	testCases := []struct {
		name          string
		inputPattern  string
		resultPath    string
		resultPattern string
	}{
		{
			name:          "FileMode",
			inputPattern:  "",
			resultPath:    "/data/corefile",
			resultPattern: "core_%e_%t.%p",
		},
		{
			name:          "ConfigWithNoNewline",
			inputPattern:  "/data/corefile2/core_%e",
			resultPath:    "/data/corefile2",
			resultPattern: "core_%e",
		},
		{
			name:          "ConfigWithOneNewline",
			inputPattern:  "/data/corefile2/core_%e\n",
			resultPath:    "/data/corefile2",
			resultPattern: "core_%e",
		},
	}

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			c := &Collector{coreFilePattern: tt.inputPattern}
			corePath, _ := c.getCoreFilePath()
			assert.Equal(t, corePath, tt.resultPath)
			assert.Equal(t, c.pattern, tt.resultPattern)
		})
	}
}

func TestCoreFileCollector_fillDimension(t *testing.T) {
	type fields struct {
		pattern   string
		isUsesPid bool
	}
	type args struct {
		filePath string
	}
	tests := []struct {
		name   string
		fields fields
		args   args
		want   beat.MapStr
		want1  bool
	}{
		{
			"路径和信号",
			fields{
				pattern:   "corefile-%E-%e-%I-%i-%s-%t",
				isUsesPid: false,
			},
			args{
				filePath: "/corefile/corefile-!tmp!test!test-test-25486-25486-8-1607502386",
			},
			beat.MapStr{
				"executable_path": "/tmp/test/test",
				"executable":      "test",
				"signal":          "SIGFPE",
				"event_time":      "1607502386",
			},
			true,
		},
		{
			"路径和信号带pid扩展名",
			fields{
				pattern:   "corefile-%E-%e-%I-%i-%s-%t",
				isUsesPid: true,
			},
			args{
				filePath: "/corefile/corefile-!tmp!test!test-test-25486-25486-8-1607502386.25486",
			},
			beat.MapStr{
				"executable_path": "/tmp/test/test",
				"executable":      "test",
				"signal":          "SIGFPE",
				"event_time":      "1607502386",
			},
			true,
		},
		{
			"pattern和文件名不匹配",
			fields{
				pattern:   "corefile-%E-%e-%I-%i-%s-%t",
				isUsesPid: false,
			},
			args{
				filePath: "/corefile/corefile-!tmp!test!t～ded。est-tes8-1607502386.25486",
			},
			beat.MapStr{},
			false,
		},
		{
			"分隔符包含正则元字符",
			fields{
				pattern:   "corefile-%E.%e\\%I~%i-%stt%t",
				isUsesPid: false,
			},
			args{
				filePath: "/corefile/corefile-!tmp!test!test.test\\25486~25486-8tt1607502386",
			},
			beat.MapStr{
				"executable_path": "/tmp/test/test",
				"executable":      "test",
				"signal":          "SIGFPE",
				"event_time":      "1607502386",
			},
			true,
		},
		{
			"分隔符和内容冲突-!",
			fields{
				pattern:   "corefile-%E!%e-%I-%i-%s-%t",
				isUsesPid: false,
			},
			args{
				filePath: "/corefile/corefile-!tmp!test!test!test-25486-25486-8-1607502386",
			},
			beat.MapStr{
				"event_time": "1607502386",
				"signal":     "SIGFPE",
			},
			true,
		},
		{
			"分隔符和内容冲突-8",
			fields{
				pattern:   "corefile-%E-%e-%I-%i8%s-%t",
				isUsesPid: false,
			},
			args{
				filePath: "/corefile/corefile-!tmp!test!test-test-25486-2548688-1607502386",
			},
			beat.MapStr{
				"event_time":      "1607502386",
				"executable":      "test",
				"executable_path": "/tmp/test/test",
			},
			true,
		},
		{
			"根据%E补充%e维度",
			fields{
				pattern:   "corefile-%E-%I-%i8%s-%t",
				isUsesPid: false,
			},
			args{
				filePath: "/corefile/corefile-!tmp!test!test1-25486-2548688-1607502386",
			},
			beat.MapStr{
				"event_time":      "1607502386",
				"executable_path": "/tmp/test/test1",
				"executable":      "test1",
			},
			true,
		},
		{
			"存在不可用的占位符",
			fields{
				pattern:   "core_%w_%e",
				isUsesPid: false,
			},
			args{
				filePath: "/data/corefile/core__demo",
			},
			beat.MapStr{
				"executable": "demo",
			},
			true,
		},
		{
			"测试将pid占位符加入",
			fields{
				pattern:   "core_%e_%p",
				isUsesPid: false,
			},
			args{
				filePath: "/data/corefile/core_demo_77190",
			},
			beat.MapStr{
				"executable": "demo",
			},
			true,
		},
		{
			"测试特意将pid占位符，而且存在不可用的占位符 加入到pattern中的情形",
			fields{
				pattern:   "core_%w_%e_%t_%p",
				isUsesPid: false,
			},
			args{
				filePath: "/data/corefile/core__demo_1616056187_77190",
			},
			beat.MapStr{
				"executable": "demo",
				"event_time": "1616056187",
			},
			true,
		},
		{
			"设置了use_pid，但是实际上已经手动匹配过了，此时不需要划分后缀",
			fields{
				pattern:   "core_%w_%e_%t_%p",
				isUsesPid: true,
			},
			args{
				filePath: "/data/corefile/core__demo_1616056187_77190",
			},
			beat.MapStr{
				"executable": "demo",
				"event_time": "1616056187",
			},
			true,
		},
		{
			"第一个自身匹配出分隔符",
			fields{
				pattern:   "core_%e_%t",
				isUsesPid: true,
			},
			args{
				filePath: "/data/corefile/core_gen_core_test_1668062442.10453",
			},
			beat.MapStr{
				"executable": "gen_core_test",
				"event_time": "1668062442",
			},
			true,
		},
		{
			"匹配出上一个分隔符造成歧义",
			fields{
				pattern:   "core_%et%h_%t",
				isUsesPid: true,
			},
			args{
				filePath: "/data/corefile/core_gen_test_centos_1668062442.10453",
			},
			beat.MapStr{
				"event_time": "1668062442",
			},
			true,
		},
		{
			"连续三个有歧义",
			fields{
				pattern:   "core_%et%ht%E_%t",
				isUsesPid: true,
			},
			args{
				filePath: "/data/corefile/core_gen_test_centos_test_path_1668062442.10453",
			},
			beat.MapStr{
				"event_time": "1668062442",
			},
			true,
		},
		{
			"匹配出前后分隔符但和前后都不匹配",
			fields{
				pattern:   "core_%t_%e_%s",
				isUsesPid: false,
			},
			args{
				filePath: "/data/corefile/core_1668062442_gen_core_test_8",
			},
			beat.MapStr{
				"executable": "gen_core_test",
				"event_time": "1668062442",
				"signal":     "SIGFPE",
			},
			true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Collector{
				pattern:   tt.fields.pattern,
				isUsesPid: tt.fields.isUsesPid,
			}
			err := c.checkPattern()
			assert.NoError(t, err, "checkPattern", tt.fields.pattern)
			got, got1 := c.fillDimension(tt.args.filePath)
			assert.Equalf(t, tt.want, got, "fillDimension(%v)", tt.args.filePath)
			assert.Equalf(t, tt.want1, got1, "fillDimension(%v)", tt.args.filePath)
		})
	}
}
