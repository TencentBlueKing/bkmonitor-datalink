// Code generated from LuceneParser.g4 by ANTLR 4.13.2. DO NOT EDIT.

package gen // LuceneParser
import "github.com/antlr4-go/antlr/v4"

// LuceneParserListener is a complete listener for a parse tree produced by LuceneParser.
type LuceneParserListener interface {
	antlr.ParseTreeListener

	// EnterTopLevelQuery is called when entering the topLevelQuery production.
	EnterTopLevelQuery(c *TopLevelQueryContext)

	// EnterQuery is called when entering the query production.
	EnterQuery(c *QueryContext)

	// EnterDisjQuery is called when entering the disjQuery production.
	EnterDisjQuery(c *DisjQueryContext)

	// EnterConjQuery is called when entering the conjQuery production.
	EnterConjQuery(c *ConjQueryContext)

	// EnterModClause is called when entering the modClause production.
	EnterModClause(c *ModClauseContext)

	// EnterModifier is called when entering the modifier production.
	EnterModifier(c *ModifierContext)

	// EnterClause is called when entering the clause production.
	EnterClause(c *ClauseContext)

	// EnterFieldRangeExpr is called when entering the fieldRangeExpr production.
	EnterFieldRangeExpr(c *FieldRangeExprContext)

	// EnterTerm is called when entering the term production.
	EnterTerm(c *TermContext)

	// EnterGroupingExpr is called when entering the groupingExpr production.
	EnterGroupingExpr(c *GroupingExprContext)

	// EnterFieldName is called when entering the fieldName production.
	EnterFieldName(c *FieldNameContext)

	// EnterTermRangeExpr is called when entering the termRangeExpr production.
	EnterTermRangeExpr(c *TermRangeExprContext)

	// EnterQuotedTerm is called when entering the quotedTerm production.
	EnterQuotedTerm(c *QuotedTermContext)

	// EnterFuzzy is called when entering the fuzzy production.
	EnterFuzzy(c *FuzzyContext)

	// ExitTopLevelQuery is called when exiting the topLevelQuery production.
	ExitTopLevelQuery(c *TopLevelQueryContext)

	// ExitQuery is called when exiting the query production.
	ExitQuery(c *QueryContext)

	// ExitDisjQuery is called when exiting the disjQuery production.
	ExitDisjQuery(c *DisjQueryContext)

	// ExitConjQuery is called when exiting the conjQuery production.
	ExitConjQuery(c *ConjQueryContext)

	// ExitModClause is called when exiting the modClause production.
	ExitModClause(c *ModClauseContext)

	// ExitModifier is called when exiting the modifier production.
	ExitModifier(c *ModifierContext)

	// ExitClause is called when exiting the clause production.
	ExitClause(c *ClauseContext)

	// ExitFieldRangeExpr is called when exiting the fieldRangeExpr production.
	ExitFieldRangeExpr(c *FieldRangeExprContext)

	// ExitTerm is called when exiting the term production.
	ExitTerm(c *TermContext)

	// ExitGroupingExpr is called when exiting the groupingExpr production.
	ExitGroupingExpr(c *GroupingExprContext)

	// ExitFieldName is called when exiting the fieldName production.
	ExitFieldName(c *FieldNameContext)

	// ExitTermRangeExpr is called when exiting the termRangeExpr production.
	ExitTermRangeExpr(c *TermRangeExprContext)

	// ExitQuotedTerm is called when exiting the quotedTerm production.
	ExitQuotedTerm(c *QuotedTermContext)

	// ExitFuzzy is called when exiting the fuzzy production.
	ExitFuzzy(c *FuzzyContext)
}
