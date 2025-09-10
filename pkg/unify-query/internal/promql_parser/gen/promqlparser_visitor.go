// Code generated from ./antlr4/PromQLParser.g4 by ANTLR 4.13.2. DO NOT EDIT.

package gen // PromQLParser
import "github.com/antlr4-go/antlr/v4"

// A complete Visitor for a parse tree produced by PromQLParser.
type PromQLParserVisitor interface {
	antlr.ParseTreeVisitor

	// VisitChildren a parse tree produced by PromQLParser#expression.
	VisitExpression(ctx *ExpressionContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#vectorOperation.
	VisitVectorOperation(ctx *VectorOperationContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#unaryOp.
	VisitUnaryOp(ctx *UnaryOpContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#powOp.
	VisitPowOp(ctx *PowOpContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#multOp.
	VisitMultOp(ctx *MultOpContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#addOp.
	VisitAddOp(ctx *AddOpContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#compareOp.
	VisitCompareOp(ctx *CompareOpContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#andUnlessOp.
	VisitAndUnlessOp(ctx *AndUnlessOpContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#orOp.
	VisitOrOp(ctx *OrOpContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#vectorMatchOp.
	VisitVectorMatchOp(ctx *VectorMatchOpContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#subqueryOp.
	VisitSubqueryOp(ctx *SubqueryOpContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#offsetOp.
	VisitOffsetOp(ctx *OffsetOpContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#vector.
	VisitVector(ctx *VectorContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#parens.
	VisitParens(ctx *ParensContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#instantSelector.
	VisitInstantSelector(ctx *InstantSelectorContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#labelMatcher.
	VisitLabelMatcher(ctx *LabelMatcherContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#labelMatcherOperator.
	VisitLabelMatcherOperator(ctx *LabelMatcherOperatorContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#labelMatcherList.
	VisitLabelMatcherList(ctx *LabelMatcherListContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#matrixSelector.
	VisitMatrixSelector(ctx *MatrixSelectorContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#offset.
	VisitOffset(ctx *OffsetContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#function_.
	VisitFunction_(ctx *Function_Context) interface{}

	// VisitChildren a parse tree produced by PromQLParser#parameter.
	VisitParameter(ctx *ParameterContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#parameterList.
	VisitParameterList(ctx *ParameterListContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#aggregation.
	VisitAggregation(ctx *AggregationContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#by.
	VisitBy(ctx *ByContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#without.
	VisitWithout(ctx *WithoutContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#grouping.
	VisitGrouping(ctx *GroupingContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#on_.
	VisitOn_(ctx *On_Context) interface{}

	// VisitChildren a parse tree produced by PromQLParser#ignoring.
	VisitIgnoring(ctx *IgnoringContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#groupLeft.
	VisitGroupLeft(ctx *GroupLeftContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#groupRight.
	VisitGroupRight(ctx *GroupRightContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#labelName.
	VisitLabelName(ctx *LabelNameContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#labelNameList.
	VisitLabelNameList(ctx *LabelNameListContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#keyword.
	VisitKeyword(ctx *KeywordContext) interface{}

	// VisitChildren a parse tree produced by PromQLParser#literal.
	VisitLiteral(ctx *LiteralContext) interface{}
}
