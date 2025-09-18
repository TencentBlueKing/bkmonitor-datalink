// Code generated from LuceneParser.g4 by ANTLR 4.13.2. DO NOT EDIT.

package gen // LuceneParser
import "github.com/antlr4-go/antlr/v4"

// BaseLuceneParserListener is a complete listener for a parse tree produced by LuceneParser.
type BaseLuceneParserListener struct{}

var _ LuceneParserListener = &BaseLuceneParserListener{}

// VisitTerminal is called when a terminal node is visited.
func (s *BaseLuceneParserListener) VisitTerminal(node antlr.TerminalNode) {}

// VisitErrorNode is called when an error node is visited.
func (s *BaseLuceneParserListener) VisitErrorNode(node antlr.ErrorNode) {}

// EnterEveryRule is called when any rule is entered.
func (s *BaseLuceneParserListener) EnterEveryRule(ctx antlr.ParserRuleContext) {}

// ExitEveryRule is called when any rule is exited.
func (s *BaseLuceneParserListener) ExitEveryRule(ctx antlr.ParserRuleContext) {}

// EnterTopLevelQuery is called when production topLevelQuery is entered.
func (s *BaseLuceneParserListener) EnterTopLevelQuery(ctx *TopLevelQueryContext) {}

// ExitTopLevelQuery is called when production topLevelQuery is exited.
func (s *BaseLuceneParserListener) ExitTopLevelQuery(ctx *TopLevelQueryContext) {}

// EnterQuery is called when production query is entered.
func (s *BaseLuceneParserListener) EnterQuery(ctx *QueryContext) {}

// ExitQuery is called when production query is exited.
func (s *BaseLuceneParserListener) ExitQuery(ctx *QueryContext) {}

// EnterDisjQuery is called when production disjQuery is entered.
func (s *BaseLuceneParserListener) EnterDisjQuery(ctx *DisjQueryContext) {}

// ExitDisjQuery is called when production disjQuery is exited.
func (s *BaseLuceneParserListener) ExitDisjQuery(ctx *DisjQueryContext) {}

// EnterConjQuery is called when production conjQuery is entered.
func (s *BaseLuceneParserListener) EnterConjQuery(ctx *ConjQueryContext) {}

// ExitConjQuery is called when production conjQuery is exited.
func (s *BaseLuceneParserListener) ExitConjQuery(ctx *ConjQueryContext) {}

// EnterModClause is called when production modClause is entered.
func (s *BaseLuceneParserListener) EnterModClause(ctx *ModClauseContext) {}

// ExitModClause is called when production modClause is exited.
func (s *BaseLuceneParserListener) ExitModClause(ctx *ModClauseContext) {}

// EnterModifier is called when production modifier is entered.
func (s *BaseLuceneParserListener) EnterModifier(ctx *ModifierContext) {}

// ExitModifier is called when production modifier is exited.
func (s *BaseLuceneParserListener) ExitModifier(ctx *ModifierContext) {}

// EnterClause is called when production clause is entered.
func (s *BaseLuceneParserListener) EnterClause(ctx *ClauseContext) {}

// ExitClause is called when production clause is exited.
func (s *BaseLuceneParserListener) ExitClause(ctx *ClauseContext) {}

// EnterFieldRangeExpr is called when production fieldRangeExpr is entered.
func (s *BaseLuceneParserListener) EnterFieldRangeExpr(ctx *FieldRangeExprContext) {}

// ExitFieldRangeExpr is called when production fieldRangeExpr is exited.
func (s *BaseLuceneParserListener) ExitFieldRangeExpr(ctx *FieldRangeExprContext) {}

// EnterTerm is called when production term is entered.
func (s *BaseLuceneParserListener) EnterTerm(ctx *TermContext) {}

// ExitTerm is called when production term is exited.
func (s *BaseLuceneParserListener) ExitTerm(ctx *TermContext) {}

// EnterGroupingExpr is called when production groupingExpr is entered.
func (s *BaseLuceneParserListener) EnterGroupingExpr(ctx *GroupingExprContext) {}

// ExitGroupingExpr is called when production groupingExpr is exited.
func (s *BaseLuceneParserListener) ExitGroupingExpr(ctx *GroupingExprContext) {}

// EnterFieldName is called when production fieldName is entered.
func (s *BaseLuceneParserListener) EnterFieldName(ctx *FieldNameContext) {}

// ExitFieldName is called when production fieldName is exited.
func (s *BaseLuceneParserListener) ExitFieldName(ctx *FieldNameContext) {}

// EnterTermRangeExpr is called when production termRangeExpr is entered.
func (s *BaseLuceneParserListener) EnterTermRangeExpr(ctx *TermRangeExprContext) {}

// ExitTermRangeExpr is called when production termRangeExpr is exited.
func (s *BaseLuceneParserListener) ExitTermRangeExpr(ctx *TermRangeExprContext) {}

// EnterQuotedTerm is called when production quotedTerm is entered.
func (s *BaseLuceneParserListener) EnterQuotedTerm(ctx *QuotedTermContext) {}

// ExitQuotedTerm is called when production quotedTerm is exited.
func (s *BaseLuceneParserListener) ExitQuotedTerm(ctx *QuotedTermContext) {}

// EnterFuzzy is called when production fuzzy is entered.
func (s *BaseLuceneParserListener) EnterFuzzy(ctx *FuzzyContext) {}

// ExitFuzzy is called when production fuzzy is exited.
func (s *BaseLuceneParserListener) ExitFuzzy(ctx *FuzzyContext) {}
