// Code generated from LuceneParser.g4 by ANTLR 4.13.1. DO NOT EDIT.

package gen // LuceneParser
import "github.com/antlr4-go/antlr/v4"

type BaseLuceneParserVisitor struct {
	*antlr.BaseParseTreeVisitor
}

func (v *BaseLuceneParserVisitor) VisitTopLevelQuery(ctx *TopLevelQueryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitQuery(ctx *QueryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitDisjQuery(ctx *DisjQueryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitConjQuery(ctx *ConjQueryContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitModClause(ctx *ModClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitModifier(ctx *ModifierContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitClause(ctx *ClauseContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitFieldRangeExpr(ctx *FieldRangeExprContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitTerm(ctx *TermContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitGroupingExpr(ctx *GroupingExprContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitFieldName(ctx *FieldNameContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitTermRangeExpr(ctx *TermRangeExprContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitQuotedTerm(ctx *QuotedTermContext) any {
	return v.VisitChildren(ctx)
}

func (v *BaseLuceneParserVisitor) VisitFuzzy(ctx *FuzzyContext) any {
	return v.VisitChildren(ctx)
}
