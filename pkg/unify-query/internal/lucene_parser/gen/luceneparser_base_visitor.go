// Code generated from LuceneParser.g4 by ANTLR 4.13.1. DO NOT EDIT.

package gen // LuceneParser
import "github.com/antlr4-go/antlr/v4"

type BaseLuceneParserVisitor struct {
	*antlr.BaseParseTreeVisitor
}

func (v *BaseLuceneParserVisitor) VisitTopLevelQuery(ctx *TopLevelQueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitQuery(ctx *QueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitDisjQuery(ctx *DisjQueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitConjQuery(ctx *ConjQueryContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitModClause(ctx *ModClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitModifier(ctx *ModifierContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitClause(ctx *ClauseContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitFieldRangeExpr(ctx *FieldRangeExprContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitTerm(ctx *TermContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitGroupingExpr(ctx *GroupingExprContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitFieldName(ctx *FieldNameContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitTermRangeExpr(ctx *TermRangeExprContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitQuotedTerm(ctx *QuotedTermContext) interface{} {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitFuzzy(ctx *FuzzyContext) interface{} {
	return v.VisitChildren(ctx)
}
