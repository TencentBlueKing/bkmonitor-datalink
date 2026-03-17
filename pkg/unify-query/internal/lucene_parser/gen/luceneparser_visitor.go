// Code generated from LuceneParser.g4 by ANTLR 4.13.1. DO NOT EDIT.

package gen // LuceneParser
import "github.com/antlr4-go/antlr/v4"

// A complete Visitor for a parse tree produced by LuceneParser.
type LuceneParserVisitor interface {
	antlr.ParseTreeVisitor

	// Visit a parse tree produced by LuceneParser#topLevelQuery.
	VisitTopLevelQuery(ctx *TopLevelQueryContext) interface{}

	// Visit a parse tree produced by LuceneParser#query.
	VisitQuery(ctx *QueryContext) interface{}

	// Visit a parse tree produced by LuceneParser#disjQuery.
	VisitDisjQuery(ctx *DisjQueryContext) interface{}

	// Visit a parse tree produced by LuceneParser#conjQuery.
	VisitConjQuery(ctx *ConjQueryContext) interface{}

	// Visit a parse tree produced by LuceneParser#modClause.
	VisitModClause(ctx *ModClauseContext) interface{}

	// Visit a parse tree produced by LuceneParser#modifier.
	VisitModifier(ctx *ModifierContext) interface{}

	// Visit a parse tree produced by LuceneParser#clause.
	VisitClause(ctx *ClauseContext) interface{}

	// Visit a parse tree produced by LuceneParser#fieldRangeExpr.
	VisitFieldRangeExpr(ctx *FieldRangeExprContext) interface{}

	// Visit a parse tree produced by LuceneParser#term.
	VisitTerm(ctx *TermContext) interface{}

	// Visit a parse tree produced by LuceneParser#groupingExpr.
	VisitGroupingExpr(ctx *GroupingExprContext) interface{}

	// Visit a parse tree produced by LuceneParser#fieldName.
	VisitFieldName(ctx *FieldNameContext) interface{}

	// Visit a parse tree produced by LuceneParser#termRangeExpr.
	VisitTermRangeExpr(ctx *TermRangeExprContext) interface{}

	// Visit a parse tree produced by LuceneParser#quotedTerm.
	VisitQuotedTerm(ctx *QuotedTermContext) interface{}

	// Visit a parse tree produced by LuceneParser#fuzzy.
	VisitFuzzy(ctx *FuzzyContext) interface{}
}
