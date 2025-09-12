// Code generated from ./antlr4/PromQLParser.g4 by ANTLR 4.13.2. DO NOT EDIT.

package gen // PromQLParser
import "github.com/antlr4-go/antlr/v4"

type BasePromQLParserVisitor struct {
	*antlr.BaseParseTreeVisitor
}

func (v *BasePromQLParserVisitor) VisitExpression(ctx *ExpressionContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitVectorOperation(ctx *VectorOperationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitUnaryOp(ctx *UnaryOpContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitPowOp(ctx *PowOpContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitMultOp(ctx *MultOpContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitAddOp(ctx *AddOpContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitCompareOp(ctx *CompareOpContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitAndUnlessOp(ctx *AndUnlessOpContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitOrOp(ctx *OrOpContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitVectorMatchOp(ctx *VectorMatchOpContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitSubqueryOp(ctx *SubqueryOpContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitOffsetOp(ctx *OffsetOpContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitVector(ctx *VectorContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitParens(ctx *ParensContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitInstantSelector(ctx *InstantSelectorContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitLabelMatcher(ctx *LabelMatcherContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitLabelMatcherOperator(ctx *LabelMatcherOperatorContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitLabelMatcherList(ctx *LabelMatcherListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitMatrixSelector(ctx *MatrixSelectorContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitOffset(ctx *OffsetContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitFunction_(ctx *Function_Context) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitParameter(ctx *ParameterContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitParameterList(ctx *ParameterListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitAggregation(ctx *AggregationContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitBy(ctx *ByContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitWithout(ctx *WithoutContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitGrouping(ctx *GroupingContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitOn_(ctx *On_Context) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitIgnoring(ctx *IgnoringContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitGroupLeft(ctx *GroupLeftContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitGroupRight(ctx *GroupRightContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitLabelName(ctx *LabelNameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitLabelNameList(ctx *LabelNameListContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitKeyword(ctx *KeywordContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitLiteral(ctx *LiteralContext) interface{} {
	return v.VisitChildren(ctx)
}
