// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package lucene_parser_old

// https://github.com/antlr/grammars-v4/tree/ed6e09ef939ee85fc7ace557461733a530452a19/antlr/antlr4/examples/grammars-v4/lucene
//go:generate antlr4 -Dlanguage=Go -no-listener -visitor -package gen *.g4 -o ../gen
//go:generate antlr4 -Dlanguage=Go -listener -no-visitor -package gen *.g4 -o ../gen
