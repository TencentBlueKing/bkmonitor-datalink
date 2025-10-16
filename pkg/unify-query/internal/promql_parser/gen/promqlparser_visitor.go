// Code generated from ./antlr4/PromQLParser.g4 by ANTLR 4.13.2. DO NOT EDIT.

package gen // PromQLParser
import "github.com/antlr4-go/antlr/v4"

// A complete Visitor for a parse tree produced by PromQLParser.
type PromQLParserVisitor interface {
	antlr.ParseTreeVisitor

	// VisitChildren a parse tree produced by PromQLParser#expression.
	VisitExpression(ctx *ExpressionContext) any

	// VisitChildren a parse tree produced by PromQLParser#vectorOperation.
	VisitVectorOperation(ctx *VectorOperationContext) any

	// VisitChildren a parse tree produced by PromQLParser#unaryOp.
	VisitUnaryOp(ctx *UnaryOpContext) any

	// VisitChildren a parse tree produced by PromQLParser#powOp.
	VisitPowOp(ctx *PowOpContext) any

	// VisitChildren a parse tree produced by PromQLParser#multOp.
	VisitMultOp(ctx *MultOpContext) any

	// VisitChildren a parse tree produced by PromQLParser#addOp.
	VisitAddOp(ctx *AddOpContext) any

	// VisitChildren a parse tree produced by PromQLParser#compareOp.
	VisitCompareOp(ctx *CompareOpContext) any

	// VisitChildren a parse tree produced by PromQLParser#andUnlessOp.
	VisitAndUnlessOp(ctx *AndUnlessOpContext) any

	// VisitChildren a parse tree produced by PromQLParser#orOp.
	VisitOrOp(ctx *OrOpContext) any

	// VisitChildren a parse tree produced by PromQLParser#vectorMatchOp.
	VisitVectorMatchOp(ctx *VectorMatchOpContext) any

	// VisitChildren a parse tree produced by PromQLParser#subqueryOp.
	VisitSubqueryOp(ctx *SubqueryOpContext) any

	// VisitChildren a parse tree produced by PromQLParser#offsetOp.
	VisitOffsetOp(ctx *OffsetOpContext) any

	// VisitChildren a parse tree produced by PromQLParser#vector.
	VisitVector(ctx *VectorContext) any

	// VisitChildren a parse tree produced by PromQLParser#parens.
	VisitParens(ctx *ParensContext) any

	// VisitChildren a parse tree produced by PromQLParser#instantSelector.
	VisitInstantSelector(ctx *InstantSelectorContext) any

	// VisitChildren a parse tree produced by PromQLParser#labelMatcher.
	VisitLabelMatcher(ctx *LabelMatcherContext) any

	// VisitChildren a parse tree produced by PromQLParser#labelMatcherOperator.
	VisitLabelMatcherOperator(ctx *LabelMatcherOperatorContext) any

	// VisitChildren a parse tree produced by PromQLParser#labelMatcherList.
	VisitLabelMatcherList(ctx *LabelMatcherListContext) any

	// VisitChildren a parse tree produced by PromQLParser#matrixSelector.
	VisitMatrixSelector(ctx *MatrixSelectorContext) any

	// VisitChildren a parse tree produced by PromQLParser#offset.
	VisitOffset(ctx *OffsetContext) any

	// VisitChildren a parse tree produced by PromQLParser#function_.
	VisitFunction_(ctx *Function_Context) any

	// VisitChildren a parse tree produced by PromQLParser#parameter.
	VisitParameter(ctx *ParameterContext) any

	// VisitChildren a parse tree produced by PromQLParser#parameterList.
	VisitParameterList(ctx *ParameterListContext) any

	// VisitChildren a parse tree produced by PromQLParser#aggregation.
	VisitAggregation(ctx *AggregationContext) any

	// VisitChildren a parse tree produced by PromQLParser#by.
	VisitBy(ctx *ByContext) any

	// VisitChildren a parse tree produced by PromQLParser#without.
	VisitWithout(ctx *WithoutContext) any

	// VisitChildren a parse tree produced by PromQLParser#grouping.
	VisitGrouping(ctx *GroupingContext) any

	// VisitChildren a parse tree produced by PromQLParser#on_.
	VisitOn_(ctx *On_Context) any

	// VisitChildren a parse tree produced by PromQLParser#ignoring.
	VisitIgnoring(ctx *IgnoringContext) any

	// VisitChildren a parse tree produced by PromQLParser#groupLeft.
	VisitGroupLeft(ctx *GroupLeftContext) any

	// VisitChildren a parse tree produced by PromQLParser#groupRight.
	VisitGroupRight(ctx *GroupRightContext) any

	// VisitChildren a parse tree produced by PromQLParser#labelName.
	VisitLabelName(ctx *LabelNameContext) any

	// VisitChildren a parse tree produced by PromQLParser#labelNameList.
	VisitLabelNameList(ctx *LabelNameListContext) any

	// VisitChildren a parse tree produced by PromQLParser#keyword.
	VisitKeyword(ctx *KeywordContext) any

	// VisitChildren a parse tree produced by PromQLParser#literal.
	VisitLiteral(ctx *LiteralContext) any
}
