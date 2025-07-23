// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package doris_parser

import (
	"context"
	"errors"
	"fmt"
	"strings"

	antlr "github.com/antlr4-go/antlr/v4"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/internal/doris_parser/gen"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

func visitChildren(visitor antlr.ParseTreeVisitor, node antlr.RuleNode) interface{} {
	for _, child := range node.GetChildren() {
		if tree, ok := child.(antlr.ParseTree); ok {
			log.Debugf(context.TODO(), `"ENTER","%T","%s"`, tree, tree.GetText())
			tree.Accept(visitor)
			log.Debugf(context.TODO(), `"EXIT","%T","%s"`, tree, tree.GetText())
		}
	}

	return nil
}

type Node interface {
	String() string
}

type defaultNode struct {
	antlr.BaseParseTreeVisitor
}

type Statement struct {
	antlr.BaseParseTreeVisitor

	selectNode antlr.ParseTreeVisitor
	errs       []error
}

func (v *Statement) SQL() string {
	if s, ok := v.selectNode.(Node); ok {
		return s.String()
	}
	return ""
}

func (v *Statement) Error() error {
	return errors.Join(v.errs...)
}

func (v *Statement) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next antlr.ParseTreeVisitor
	next = v

	switch ctx.(type) {
	case *gen.NamedExpressionSeqContext:
		v.selectNode = &SelectNode{}
		next = v.selectNode
	}
	return visitChildren(next, ctx)
}

type SelectNode struct {
	antlr.BaseParseTreeVisitor

	fieldsNode []antlr.ParseTreeVisitor
}

func (v *SelectNode) String() string {
	var ns []string
	for _, fn := range v.fieldsNode {
		if s, ok := fn.(Node); ok {
			ns = append(ns, s.String())
		}
	}

	if len(ns) > 0 {
		return fmt.Sprintf("SELECT %s", strings.Join(ns, ", "))
	}

	return ""
}

func (v *SelectNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next antlr.ParseTreeVisitor
	next = v

	switch ctx.(type) {
	case *gen.NamedExpressionContext:
		fn := &FieldNode{}
		next = fn
		v.fieldsNode = append(v.fieldsNode, fn)
	}
	return visitChildren(next, ctx)
}

type FieldNode struct {
	antlr.BaseParseTreeVisitor

	functionNode antlr.ParseTreeVisitor
	as           antlr.ParseTreeVisitor
}

func (v *FieldNode) String() string {
	var result string
	if s, ok := v.functionNode.(Node); ok {
		result = s.String()
	}

	if as, ok := v.as.(Node); ok {
		result = fmt.Sprintf("%s AS %s", result, as.String())
	}

	return result
}

func (v *FieldNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next antlr.ParseTreeVisitor
	next = v

	switch n := ctx.(type) {
	case *gen.CastContext:
		v.functionNode = &CastNode{}
		next = v.functionNode
	case *gen.FunctionCallContext:
		v.functionNode = &FunctionNode{}
		next = v.functionNode
	case *gen.ColumnReferenceContext:
		v.functionNode = &FunctionNode{
			Value: &StringNode{Name: n.GetText()},
		}
	case *gen.ConstantDefaultContext:
		if v.functionNode != nil {
			if fn, ok := v.functionNode.(*FunctionNode); ok {
				fn.Args = append(fn.Args, &StringNode{Name: n.GetText()})
			}
		}
	case *gen.IdentifierOrTextContext:
		v.as = &StringNode{
			Name: n.GetText(),
		}
	case *gen.StarContext:
		v.functionNode = &StringNode{Name: n.GetText()}
	}
	return visitChildren(next, ctx)
}

type FunctionNode struct {
	antlr.BaseParseTreeVisitor
	FuncName string
	Value    antlr.ParseTreeVisitor
	Args     []antlr.ParseTreeVisitor
}

func (v *FunctionNode) String() string {

	var result string
	if s, ok := v.Value.(Node); ok {
		result = s.String()
	}

	var cols []string
	for _, val := range v.Args {
		if s, ok := val.(Node); ok {
			cols = append(cols, s.String())
		}
	}
	if len(cols) > 0 {
		result = fmt.Sprintf("%s[%s]", result, strings.Join(cols, "]["))
	}

	if v.FuncName != "" {
		result = fmt.Sprintf("%s(%s)", v.FuncName, result)
	}
	return result
}

func (v *FunctionNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next antlr.ParseTreeVisitor
	next = v

	switch n := ctx.(type) {
	case *gen.CastContext:
		v.Value = &CastNode{}
		next = v.Value
	case *gen.FunctionCallContext:
		v.Value = &FunctionNode{}
		next = v.Value
	case *gen.FunctionIdentifierContext:
		v.FuncName = n.GetText()
	case *gen.ColumnReferenceContext:
		v.Value = &StringNode{Name: n.GetText()}
	case *gen.ConstantDefaultContext:
		v.Args = append(v.Args, &StringNode{Name: n.GetText()})
	case *gen.StarContext:
		v.Value = &StringNode{Name: n.GetText()}
		next = v.Value
	}
	return visitChildren(next, ctx)
}

type CastNode struct {
	antlr.BaseParseTreeVisitor
	Value antlr.ParseTreeVisitor
	As    antlr.ParseTreeVisitor
	Args  []antlr.ParseTreeVisitor
}

func (v *CastNode) String() string {
	var result string
	if s, ok := v.Value.(Node); ok {
		result = s.String()
	}
	var cols []string
	for _, val := range v.Args {
		if s, ok := val.(Node); ok {
			cols = append(cols, s.String())
		}
	}
	if len(cols) > 0 {
		result = fmt.Sprintf("%s[%s]", result, strings.Join(cols, "]["))
	}

	if s, ok := v.As.(Node); ok {
		result = fmt.Sprintf("CAST(%s AS %s)", result, s.String())
	}
	return result
}

func (v *CastNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	var next antlr.ParseTreeVisitor
	next = v

	switch n := ctx.(type) {
	case *gen.CastDataTypeContext:
		v.As = &StringNode{
			Name: n.GetText(),
		}
		next = v.As
	case *gen.FunctionCallContext:
		v.Value = &FunctionNode{}
		next = v.Value
	case *gen.ColumnReferenceContext:
		v.Value = &StringNode{Name: n.GetText()}
	case *gen.ConstantDefaultContext:
		v.Args = append(v.Args, &StringNode{Name: n.GetText()})
	case *gen.StarContext:
		v.Value = &StringNode{Name: n.GetText()}
		next = v.Value
	}
	return visitChildren(next, ctx)
}

type StringNode struct {
	antlr.BaseParseTreeVisitor
	Name string
}

func (v *StringNode) String() string {
	return v.Name
}

func (v *StringNode) VisitChildren(ctx antlr.RuleNode) interface{} {
	return visitChildren(v, ctx)
}
