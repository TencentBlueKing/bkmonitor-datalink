// Code generated from LuceneParser.g4 by ANTLR 4.13.1. DO NOT EDIT.

package gen // LuceneParser
import "github.com/antlr4-go/antlr/v4"

// A complete Visitor for a parse tree produced by LuceneParser.
type LuceneParserVisitor interface {
	antlr.ParseTreeVisitor

	// Visit a parse tree produced by LuceneParser#topLevelQuery.
	VisitTopLevelQuery(ctx *TopLevelQueryContext) any

	// Visit a parse tree produced by LuceneParser#query.
	VisitQuery(ctx *QueryContext) any

	// Visit a parse tree produced by LuceneParser#disjQuery.
	VisitDisjQuery(ctx *DisjQueryContext) any

	// Visit a parse tree produced by LuceneParser#conjQuery.
	VisitConjQuery(ctx *ConjQueryContext) any

	// Visit a parse tree produced by LuceneParser#modClause.
	VisitModClause(ctx *ModClauseContext) any

	// Visit a parse tree produced by LuceneParser#modifier.
	VisitModifier(ctx *ModifierContext) any

	// Visit a parse tree produced by LuceneParser#clause.
	VisitClause(ctx *ClauseContext) any

	// Visit a parse tree produced by LuceneParser#fieldRangeExpr.
	VisitFieldRangeExpr(ctx *FieldRangeExprContext) any

	// Visit a parse tree produced by LuceneParser#term.
	VisitTerm(ctx *TermContext) any

	// Visit a parse tree produced by LuceneParser#groupingExpr.
	VisitGroupingExpr(ctx *GroupingExprContext) any

	// Visit a parse tree produced by LuceneParser#fieldName.
	VisitFieldName(ctx *FieldNameContext) any

	// Visit a parse tree produced by LuceneParser#termRangeExpr.
	VisitTermRangeExpr(ctx *TermRangeExprContext) any

	// Visit a parse tree produced by LuceneParser#quotedTerm.
	VisitQuotedTerm(ctx *QuotedTermContext) any

	// Visit a parse tree produced by LuceneParser#fuzzy.
	VisitFuzzy(ctx *FuzzyContext) any
}
