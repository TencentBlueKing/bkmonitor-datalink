// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package doris_parser

// https://github.com/apache/doris/tree/master/fe/fe-core/src/main/antlr4/org/apache/doris/nereids
//go:generate antlr4 -Dlanguage=Go -no-listener -visitor -package gen *.g4 -o ../gen
//go:generate antlr4 -Dlanguage=Go -listener -no-visitor -package gen *.g4 -o ../gen

// for lexer.g4
var has_unclosed_bracketed_comment = false

func isValidDecimal(input string) bool {
	return true
}

func markUnclosedComment() {
	has_unclosed_bracketed_comment = true
}

// for parser.g4
var ansiSQLSyntax = false
