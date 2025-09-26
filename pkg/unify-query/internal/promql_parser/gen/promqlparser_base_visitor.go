// Code generated from ./antlr4/PromQLParser.g4 by ANTLR 4.13.2. DO NOT EDIT.

package gen // PromQLParser
import "github.com/antlr4-go/antlr/v4"

type BasePromQLParserVisitor struct {
	*antlr.BaseParseTreeVisitor
}

func (v *BasePromQLParserVisitor) VisitExpression(ctx *ExpressionContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitVectorOperation(ctx *VectorOperationContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitUnaryOp(ctx *UnaryOpContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitPowOp(ctx *PowOpContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitMultOp(ctx *MultOpContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitAddOp(ctx *AddOpContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitCompareOp(ctx *CompareOpContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitAndUnlessOp(ctx *AndUnlessOpContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitOrOp(ctx *OrOpContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitVectorMatchOp(ctx *VectorMatchOpContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitSubqueryOp(ctx *SubqueryOpContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitOffsetOp(ctx *OffsetOpContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitVector(ctx *VectorContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitParens(ctx *ParensContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitInstantSelector(ctx *InstantSelectorContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitLabelMatcher(ctx *LabelMatcherContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitLabelMatcherOperator(ctx *LabelMatcherOperatorContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitLabelMatcherList(ctx *LabelMatcherListContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitMatrixSelector(ctx *MatrixSelectorContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitOffset(ctx *OffsetContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitFunction_(ctx *Function_Context) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitParameter(ctx *ParameterContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitParameterList(ctx *ParameterListContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitAggregation(ctx *AggregationContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitBy(ctx *ByContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitWithout(ctx *WithoutContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitGrouping(ctx *GroupingContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitOn_(ctx *On_Context) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitIgnoring(ctx *IgnoringContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitGroupLeft(ctx *GroupLeftContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitGroupRight(ctx *GroupRightContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitLabelName(ctx *LabelNameContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitLabelNameList(ctx *LabelNameListContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitKeyword(ctx *KeywordContext) any {
	return v.VisitChildren(ctx)
}

func (v *BasePromQLParserVisitor) VisitLiteral(ctx *LiteralContext) any {
	return v.VisitChildren(ctx)
}
