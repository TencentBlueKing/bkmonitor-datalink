// Code generated from LuceneParser.g4 by ANTLR 4.13.1. DO NOT EDIT.

package gen // LuceneParser
import (
	"fmt"
	"strconv"
	"sync"

	"github.com/antlr4-go/antlr/v4"
)

// Suppress unused import errors
var _ = fmt.Printf
var _ = strconv.Itoa
var _ = sync.Once{}

type LuceneParser struct {
	*antlr.BaseParser
}

var LuceneParserParserStaticData struct {
	once                   sync.Once
	serializedATN          []int32
	LiteralNames           []string
	SymbolicNames          []string
	RuleNames              []string
	PredictionContextCache *antlr.PredictionContextCache
	atn                    *antlr.ATN
	decisionToDFA          []*antlr.DFA
}

func luceneparserParserInit() {
	staticData := &LuceneParserParserStaticData
	staticData.LiteralNames = []string{
		"", "", "", "", "'fn:'", "'+'", "'-'", "'('", "')'", "':'", "'='", "'<'",
		"'<='", "'>'", "'>='", "'^'", "'~'", "", "", "", "", "'['", "'{'", "",
		"", "", "", "'after'", "'before'", "", "'containing'", "'extend'", "'or'",
		"", "", "", "", "", "", "", "'ordered'", "'overlapping'", "'phrase'",
		"'unordered'", "", "'wildcard'", "'within'", "", "", "']'", "'}'",
	}
	staticData.SymbolicNames = []string{
		"", "AND", "OR", "NOT", "FN_PREFIX", "PLUS", "MINUS", "LPAREN", "RPAREN",
		"OP_COLON", "OP_EQUAL", "OP_LESSTHAN", "OP_LESSTHANEQ", "OP_MORETHAN",
		"OP_MORETHANEQ", "CARAT", "TILDE", "QUOTED", "NUMBER", "TERM", "REGEXPTERM",
		"RANGEIN_START", "RANGEEX_START", "DEFAULT_SKIP", "UNKNOWN", "F_SKIP",
		"ATLEAST", "AFTER", "BEFORE", "CONTAINED_BY", "CONTAINING", "EXTEND",
		"FN_OR", "FUZZYTERM", "MAXGAPS", "MAXWIDTH", "NON_OVERLAPPING", "NOT_CONTAINED_BY",
		"NOT_CONTAINING", "NOT_WITHIN", "ORDERED", "OVERLAPPING", "PHRASE",
		"UNORDERED", "UNORDERED_NO_OVERLAPS", "WILDCARD", "WITHIN", "R_SKIP",
		"RANGE_TO", "RANGEIN_END", "RANGEEX_END", "RANGE_QUOTED", "RANGE_GOOP",
	}
	staticData.RuleNames = []string{
		"topLevelQuery", "query", "disjQuery", "conjQuery", "modClause", "modifier",
		"clause", "fieldRangeExpr", "term", "groupingExpr", "fieldName", "termRangeExpr",
		"quotedTerm", "fuzzy",
	}
	staticData.PredictionContextCache = antlr.NewPredictionContextCache()
	staticData.serializedATN = []int32{
		4, 1, 52, 148, 2, 0, 7, 0, 2, 1, 7, 1, 2, 2, 7, 2, 2, 3, 7, 3, 2, 4, 7,
		4, 2, 5, 7, 5, 2, 6, 7, 6, 2, 7, 7, 7, 2, 8, 7, 8, 2, 9, 7, 9, 2, 10, 7,
		10, 2, 11, 7, 11, 2, 12, 7, 12, 2, 13, 7, 13, 1, 0, 1, 0, 1, 0, 1, 1, 4,
		1, 33, 8, 1, 11, 1, 12, 1, 34, 1, 2, 1, 2, 1, 2, 5, 2, 40, 8, 2, 10, 2,
		12, 2, 43, 9, 2, 1, 2, 3, 2, 46, 8, 2, 1, 3, 1, 3, 1, 3, 5, 3, 51, 8, 3,
		10, 3, 12, 3, 54, 9, 3, 1, 3, 3, 3, 57, 8, 3, 1, 4, 3, 4, 60, 8, 4, 1,
		4, 1, 4, 1, 5, 1, 5, 1, 6, 1, 6, 1, 6, 1, 6, 3, 6, 70, 8, 6, 1, 6, 1, 6,
		3, 6, 74, 8, 6, 3, 6, 76, 8, 6, 1, 7, 1, 7, 3, 7, 80, 8, 7, 1, 7, 1, 7,
		1, 7, 1, 8, 1, 8, 1, 8, 1, 8, 3, 8, 89, 8, 8, 1, 8, 1, 8, 1, 8, 3, 8, 94,
		8, 8, 1, 8, 1, 8, 1, 8, 3, 8, 99, 8, 8, 1, 8, 1, 8, 1, 8, 3, 8, 104, 8,
		8, 1, 8, 1, 8, 1, 8, 3, 8, 109, 8, 8, 3, 8, 111, 8, 8, 1, 8, 1, 8, 1, 8,
		1, 8, 3, 8, 117, 8, 8, 5, 8, 119, 8, 8, 10, 8, 12, 8, 122, 9, 8, 1, 9,
		1, 9, 1, 9, 1, 9, 1, 9, 3, 9, 129, 8, 9, 1, 10, 1, 10, 1, 11, 1, 11, 1,
		11, 1, 11, 1, 11, 1, 11, 1, 12, 1, 12, 1, 12, 3, 12, 142, 8, 12, 1, 13,
		1, 13, 3, 13, 146, 8, 13, 1, 13, 0, 1, 16, 14, 0, 2, 4, 6, 8, 10, 12, 14,
		16, 18, 20, 22, 24, 26, 0, 7, 2, 0, 3, 3, 5, 6, 1, 0, 9, 10, 1, 0, 11,
		14, 1, 0, 17, 19, 1, 0, 21, 22, 2, 0, 48, 48, 51, 52, 1, 0, 49, 50, 157,
		0, 28, 1, 0, 0, 0, 2, 32, 1, 0, 0, 0, 4, 36, 1, 0, 0, 0, 6, 47, 1, 0, 0,
		0, 8, 59, 1, 0, 0, 0, 10, 63, 1, 0, 0, 0, 12, 75, 1, 0, 0, 0, 14, 77, 1,
		0, 0, 0, 16, 110, 1, 0, 0, 0, 18, 123, 1, 0, 0, 0, 20, 130, 1, 0, 0, 0,
		22, 132, 1, 0, 0, 0, 24, 138, 1, 0, 0, 0, 26, 143, 1, 0, 0, 0, 28, 29,
		3, 2, 1, 0, 29, 30, 5, 0, 0, 1, 30, 1, 1, 0, 0, 0, 31, 33, 3, 4, 2, 0,
		32, 31, 1, 0, 0, 0, 33, 34, 1, 0, 0, 0, 34, 32, 1, 0, 0, 0, 34, 35, 1,
		0, 0, 0, 35, 3, 1, 0, 0, 0, 36, 41, 3, 6, 3, 0, 37, 38, 5, 2, 0, 0, 38,
		40, 3, 6, 3, 0, 39, 37, 1, 0, 0, 0, 40, 43, 1, 0, 0, 0, 41, 39, 1, 0, 0,
		0, 41, 42, 1, 0, 0, 0, 42, 45, 1, 0, 0, 0, 43, 41, 1, 0, 0, 0, 44, 46,
		5, 2, 0, 0, 45, 44, 1, 0, 0, 0, 45, 46, 1, 0, 0, 0, 46, 5, 1, 0, 0, 0,
		47, 52, 3, 8, 4, 0, 48, 49, 5, 1, 0, 0, 49, 51, 3, 8, 4, 0, 50, 48, 1,
		0, 0, 0, 51, 54, 1, 0, 0, 0, 52, 50, 1, 0, 0, 0, 52, 53, 1, 0, 0, 0, 53,
		56, 1, 0, 0, 0, 54, 52, 1, 0, 0, 0, 55, 57, 5, 1, 0, 0, 56, 55, 1, 0, 0,
		0, 56, 57, 1, 0, 0, 0, 57, 7, 1, 0, 0, 0, 58, 60, 3, 10, 5, 0, 59, 58,
		1, 0, 0, 0, 59, 60, 1, 0, 0, 0, 60, 61, 1, 0, 0, 0, 61, 62, 3, 12, 6, 0,
		62, 9, 1, 0, 0, 0, 63, 64, 7, 0, 0, 0, 64, 11, 1, 0, 0, 0, 65, 76, 3, 14,
		7, 0, 66, 67, 3, 20, 10, 0, 67, 68, 7, 1, 0, 0, 68, 70, 1, 0, 0, 0, 69,
		66, 1, 0, 0, 0, 69, 70, 1, 0, 0, 0, 70, 73, 1, 0, 0, 0, 71, 74, 3, 16,
		8, 0, 72, 74, 3, 18, 9, 0, 73, 71, 1, 0, 0, 0, 73, 72, 1, 0, 0, 0, 74,
		76, 1, 0, 0, 0, 75, 65, 1, 0, 0, 0, 75, 69, 1, 0, 0, 0, 76, 13, 1, 0, 0,
		0, 77, 79, 3, 20, 10, 0, 78, 80, 5, 9, 0, 0, 79, 78, 1, 0, 0, 0, 79, 80,
		1, 0, 0, 0, 80, 81, 1, 0, 0, 0, 81, 82, 7, 2, 0, 0, 82, 83, 7, 3, 0, 0,
		83, 15, 1, 0, 0, 0, 84, 85, 6, 8, -1, 0, 85, 88, 5, 20, 0, 0, 86, 87, 5,
		15, 0, 0, 87, 89, 5, 18, 0, 0, 88, 86, 1, 0, 0, 0, 88, 89, 1, 0, 0, 0,
		89, 111, 1, 0, 0, 0, 90, 93, 3, 22, 11, 0, 91, 92, 5, 15, 0, 0, 92, 94,
		5, 18, 0, 0, 93, 91, 1, 0, 0, 0, 93, 94, 1, 0, 0, 0, 94, 111, 1, 0, 0,
		0, 95, 98, 3, 24, 12, 0, 96, 97, 5, 15, 0, 0, 97, 99, 5, 18, 0, 0, 98,
		96, 1, 0, 0, 0, 98, 99, 1, 0, 0, 0, 99, 111, 1, 0, 0, 0, 100, 103, 5, 18,
		0, 0, 101, 102, 5, 15, 0, 0, 102, 104, 5, 18, 0, 0, 103, 101, 1, 0, 0,
		0, 103, 104, 1, 0, 0, 0, 104, 111, 1, 0, 0, 0, 105, 108, 5, 19, 0, 0, 106,
		107, 5, 15, 0, 0, 107, 109, 5, 18, 0, 0, 108, 106, 1, 0, 0, 0, 108, 109,
		1, 0, 0, 0, 109, 111, 1, 0, 0, 0, 110, 84, 1, 0, 0, 0, 110, 90, 1, 0, 0,
		0, 110, 95, 1, 0, 0, 0, 110, 100, 1, 0, 0, 0, 110, 105, 1, 0, 0, 0, 111,
		120, 1, 0, 0, 0, 112, 113, 10, 6, 0, 0, 113, 116, 3, 26, 13, 0, 114, 115,
		5, 15, 0, 0, 115, 117, 5, 18, 0, 0, 116, 114, 1, 0, 0, 0, 116, 117, 1,
		0, 0, 0, 117, 119, 1, 0, 0, 0, 118, 112, 1, 0, 0, 0, 119, 122, 1, 0, 0,
		0, 120, 118, 1, 0, 0, 0, 120, 121, 1, 0, 0, 0, 121, 17, 1, 0, 0, 0, 122,
		120, 1, 0, 0, 0, 123, 124, 5, 7, 0, 0, 124, 125, 3, 2, 1, 0, 125, 128,
		5, 8, 0, 0, 126, 127, 5, 15, 0, 0, 127, 129, 5, 18, 0, 0, 128, 126, 1,
		0, 0, 0, 128, 129, 1, 0, 0, 0, 129, 19, 1, 0, 0, 0, 130, 131, 5, 19, 0,
		0, 131, 21, 1, 0, 0, 0, 132, 133, 7, 4, 0, 0, 133, 134, 7, 5, 0, 0, 134,
		135, 5, 48, 0, 0, 135, 136, 7, 5, 0, 0, 136, 137, 7, 6, 0, 0, 137, 23,
		1, 0, 0, 0, 138, 141, 5, 17, 0, 0, 139, 140, 5, 15, 0, 0, 140, 142, 5,
		18, 0, 0, 141, 139, 1, 0, 0, 0, 141, 142, 1, 0, 0, 0, 142, 25, 1, 0, 0,
		0, 143, 145, 5, 16, 0, 0, 144, 146, 5, 18, 0, 0, 145, 144, 1, 0, 0, 0,
		145, 146, 1, 0, 0, 0, 146, 27, 1, 0, 0, 0, 21, 34, 41, 45, 52, 56, 59,
		69, 73, 75, 79, 88, 93, 98, 103, 108, 110, 116, 120, 128, 141, 145,
	}
	deserializer := antlr.NewATNDeserializer(nil)
	staticData.atn = deserializer.Deserialize(staticData.serializedATN)
	atn := staticData.atn
	staticData.decisionToDFA = make([]*antlr.DFA, len(atn.DecisionToState))
	decisionToDFA := staticData.decisionToDFA
	for index, state := range atn.DecisionToState {
		decisionToDFA[index] = antlr.NewDFA(state, index)
	}
}

// LuceneParserInit initializes any static state used to implement LuceneParser. By default the
// static state used to implement the parser is lazily initialized during the first call to
// NewLuceneParser(). You can call this function if you wish to initialize the static state ahead
// of time.
func LuceneParserInit() {
	staticData := &LuceneParserParserStaticData
	staticData.once.Do(luceneparserParserInit)
}

// NewLuceneParser produces a new parser instance for the optional input antlr.TokenStream.
func NewLuceneParser(input antlr.TokenStream) *LuceneParser {
	LuceneParserInit()
	this := new(LuceneParser)
	this.BaseParser = antlr.NewBaseParser(input)
	staticData := &LuceneParserParserStaticData
	this.Interpreter = antlr.NewParserATNSimulator(this, staticData.atn, staticData.decisionToDFA, staticData.PredictionContextCache)
	this.RuleNames = staticData.RuleNames
	this.LiteralNames = staticData.LiteralNames
	this.SymbolicNames = staticData.SymbolicNames
	this.GrammarFileName = "LuceneParser.g4"

	return this
}

// LuceneParser tokens.
const (
	LuceneParserEOF                   = antlr.TokenEOF
	LuceneParserAND                   = 1
	LuceneParserOR                    = 2
	LuceneParserNOT                   = 3
	LuceneParserFN_PREFIX             = 4
	LuceneParserPLUS                  = 5
	LuceneParserMINUS                 = 6
	LuceneParserLPAREN                = 7
	LuceneParserRPAREN                = 8
	LuceneParserOP_COLON              = 9
	LuceneParserOP_EQUAL              = 10
	LuceneParserOP_LESSTHAN           = 11
	LuceneParserOP_LESSTHANEQ         = 12
	LuceneParserOP_MORETHAN           = 13
	LuceneParserOP_MORETHANEQ         = 14
	LuceneParserCARAT                 = 15
	LuceneParserTILDE                 = 16
	LuceneParserQUOTED                = 17
	LuceneParserNUMBER                = 18
	LuceneParserTERM                  = 19
	LuceneParserREGEXPTERM            = 20
	LuceneParserRANGEIN_START         = 21
	LuceneParserRANGEEX_START         = 22
	LuceneParserDEFAULT_SKIP          = 23
	LuceneParserUNKNOWN               = 24
	LuceneParserF_SKIP                = 25
	LuceneParserATLEAST               = 26
	LuceneParserAFTER                 = 27
	LuceneParserBEFORE                = 28
	LuceneParserCONTAINED_BY          = 29
	LuceneParserCONTAINING            = 30
	LuceneParserEXTEND                = 31
	LuceneParserFN_OR                 = 32
	LuceneParserFUZZYTERM             = 33
	LuceneParserMAXGAPS               = 34
	LuceneParserMAXWIDTH              = 35
	LuceneParserNON_OVERLAPPING       = 36
	LuceneParserNOT_CONTAINED_BY      = 37
	LuceneParserNOT_CONTAINING        = 38
	LuceneParserNOT_WITHIN            = 39
	LuceneParserORDERED               = 40
	LuceneParserOVERLAPPING           = 41
	LuceneParserPHRASE                = 42
	LuceneParserUNORDERED             = 43
	LuceneParserUNORDERED_NO_OVERLAPS = 44
	LuceneParserWILDCARD              = 45
	LuceneParserWITHIN                = 46
	LuceneParserR_SKIP                = 47
	LuceneParserRANGE_TO              = 48
	LuceneParserRANGEIN_END           = 49
	LuceneParserRANGEEX_END           = 50
	LuceneParserRANGE_QUOTED          = 51
	LuceneParserRANGE_GOOP            = 52
)

// LuceneParser rules.
const (
	LuceneParserRULE_topLevelQuery  = 0
	LuceneParserRULE_query          = 1
	LuceneParserRULE_disjQuery      = 2
	LuceneParserRULE_conjQuery      = 3
	LuceneParserRULE_modClause      = 4
	LuceneParserRULE_modifier       = 5
	LuceneParserRULE_clause         = 6
	LuceneParserRULE_fieldRangeExpr = 7
	LuceneParserRULE_term           = 8
	LuceneParserRULE_groupingExpr   = 9
	LuceneParserRULE_fieldName      = 10
	LuceneParserRULE_termRangeExpr  = 11
	LuceneParserRULE_quotedTerm     = 12
	LuceneParserRULE_fuzzy          = 13
)

// ITopLevelQueryContext is an interface to support dynamic dispatch.
type ITopLevelQueryContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	Query() IQueryContext
	EOF() antlr.TerminalNode

	// IsTopLevelQueryContext differentiates from other interfaces.
	IsTopLevelQueryContext()
}

type TopLevelQueryContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyTopLevelQueryContext() *TopLevelQueryContext {
	var p = new(TopLevelQueryContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_topLevelQuery
	return p
}

func InitEmptyTopLevelQueryContext(p *TopLevelQueryContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_topLevelQuery
}

func (*TopLevelQueryContext) IsTopLevelQueryContext() {}

func NewTopLevelQueryContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *TopLevelQueryContext {
	var p = new(TopLevelQueryContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = LuceneParserRULE_topLevelQuery

	return p
}

func (s *TopLevelQueryContext) GetParser() antlr.Parser { return s.parser }

func (s *TopLevelQueryContext) Query() IQueryContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IQueryContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IQueryContext)
}

func (s *TopLevelQueryContext) EOF() antlr.TerminalNode {
	return s.GetToken(LuceneParserEOF, 0)
}

func (s *TopLevelQueryContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *TopLevelQueryContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *TopLevelQueryContext) Accept(visitor antlr.ParseTreeVisitor) any {
	switch t := visitor.(type) {
	case LuceneParserVisitor:
		return t.VisitTopLevelQuery(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *LuceneParser) TopLevelQuery() (localctx ITopLevelQueryContext) {
	localctx = NewTopLevelQueryContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 0, LuceneParserRULE_topLevelQuery)
	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(28)
		p.Query()
	}
	{
		p.SetState(29)
		p.Match(LuceneParserEOF)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IQueryContext is an interface to support dynamic dispatch.
type IQueryContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	AllDisjQuery() []IDisjQueryContext
	DisjQuery(i int) IDisjQueryContext

	// IsQueryContext differentiates from other interfaces.
	IsQueryContext()
}

type QueryContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyQueryContext() *QueryContext {
	var p = new(QueryContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_query
	return p
}

func InitEmptyQueryContext(p *QueryContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_query
}

func (*QueryContext) IsQueryContext() {}

func NewQueryContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *QueryContext {
	var p = new(QueryContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = LuceneParserRULE_query

	return p
}

func (s *QueryContext) GetParser() antlr.Parser { return s.parser }

func (s *QueryContext) AllDisjQuery() []IDisjQueryContext {
	children := s.GetChildren()
	len := 0
	for _, ctx := range children {
		if _, ok := ctx.(IDisjQueryContext); ok {
			len++
		}
	}

	tst := make([]IDisjQueryContext, len)
	i := 0
	for _, ctx := range children {
		if t, ok := ctx.(IDisjQueryContext); ok {
			tst[i] = t.(IDisjQueryContext)
			i++
		}
	}

	return tst
}

func (s *QueryContext) DisjQuery(i int) IDisjQueryContext {
	var t antlr.RuleContext
	j := 0
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IDisjQueryContext); ok {
			if j == i {
				t = ctx.(antlr.RuleContext)
				break
			}
			j++
		}
	}

	if t == nil {
		return nil
	}

	return t.(IDisjQueryContext)
}

func (s *QueryContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *QueryContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *QueryContext) Accept(visitor antlr.ParseTreeVisitor) any {
	switch t := visitor.(type) {
	case LuceneParserVisitor:
		return t.VisitQuery(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *LuceneParser) Query() (localctx IQueryContext) {
	localctx = NewQueryContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 2, LuceneParserRULE_query)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	p.SetState(32)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	for ok := true; ok; ok = ((int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&8257768) != 0) {
		{
			p.SetState(31)
			p.DisjQuery()
		}

		p.SetState(34)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_la = p.GetTokenStream().LA(1)
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IDisjQueryContext is an interface to support dynamic dispatch.
type IDisjQueryContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	AllConjQuery() []IConjQueryContext
	ConjQuery(i int) IConjQueryContext
	AllOR() []antlr.TerminalNode
	OR(i int) antlr.TerminalNode

	// IsDisjQueryContext differentiates from other interfaces.
	IsDisjQueryContext()
}

type DisjQueryContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyDisjQueryContext() *DisjQueryContext {
	var p = new(DisjQueryContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_disjQuery
	return p
}

func InitEmptyDisjQueryContext(p *DisjQueryContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_disjQuery
}

func (*DisjQueryContext) IsDisjQueryContext() {}

func NewDisjQueryContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *DisjQueryContext {
	var p = new(DisjQueryContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = LuceneParserRULE_disjQuery

	return p
}

func (s *DisjQueryContext) GetParser() antlr.Parser { return s.parser }

func (s *DisjQueryContext) AllConjQuery() []IConjQueryContext {
	children := s.GetChildren()
	len := 0
	for _, ctx := range children {
		if _, ok := ctx.(IConjQueryContext); ok {
			len++
		}
	}

	tst := make([]IConjQueryContext, len)
	i := 0
	for _, ctx := range children {
		if t, ok := ctx.(IConjQueryContext); ok {
			tst[i] = t.(IConjQueryContext)
			i++
		}
	}

	return tst
}

func (s *DisjQueryContext) ConjQuery(i int) IConjQueryContext {
	var t antlr.RuleContext
	j := 0
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IConjQueryContext); ok {
			if j == i {
				t = ctx.(antlr.RuleContext)
				break
			}
			j++
		}
	}

	if t == nil {
		return nil
	}

	return t.(IConjQueryContext)
}

func (s *DisjQueryContext) AllOR() []antlr.TerminalNode {
	return s.GetTokens(LuceneParserOR)
}

func (s *DisjQueryContext) OR(i int) antlr.TerminalNode {
	return s.GetToken(LuceneParserOR, i)
}

func (s *DisjQueryContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *DisjQueryContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *DisjQueryContext) Accept(visitor antlr.ParseTreeVisitor) any {
	switch t := visitor.(type) {
	case LuceneParserVisitor:
		return t.VisitDisjQuery(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *LuceneParser) DisjQuery() (localctx IDisjQueryContext) {
	localctx = NewDisjQueryContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 4, LuceneParserRULE_disjQuery)
	var _la int

	var _alt int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(36)
		p.ConjQuery()
	}
	p.SetState(41)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_alt = p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 1, p.GetParserRuleContext())
	if p.HasError() {
		goto errorExit
	}
	for _alt != 2 && _alt != antlr.ATNInvalidAltNumber {
		if _alt == 1 {
			{
				p.SetState(37)
				p.Match(LuceneParserOR)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(38)
				p.ConjQuery()
			}

		}
		p.SetState(43)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_alt = p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 1, p.GetParserRuleContext())
		if p.HasError() {
			goto errorExit
		}
	}
	p.SetState(45)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == LuceneParserOR {
		{
			p.SetState(44)
			p.Match(LuceneParserOR)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IConjQueryContext is an interface to support dynamic dispatch.
type IConjQueryContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	AllModClause() []IModClauseContext
	ModClause(i int) IModClauseContext
	AllAND() []antlr.TerminalNode
	AND(i int) antlr.TerminalNode

	// IsConjQueryContext differentiates from other interfaces.
	IsConjQueryContext()
}

type ConjQueryContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyConjQueryContext() *ConjQueryContext {
	var p = new(ConjQueryContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_conjQuery
	return p
}

func InitEmptyConjQueryContext(p *ConjQueryContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_conjQuery
}

func (*ConjQueryContext) IsConjQueryContext() {}

func NewConjQueryContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *ConjQueryContext {
	var p = new(ConjQueryContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = LuceneParserRULE_conjQuery

	return p
}

func (s *ConjQueryContext) GetParser() antlr.Parser { return s.parser }

func (s *ConjQueryContext) AllModClause() []IModClauseContext {
	children := s.GetChildren()
	len := 0
	for _, ctx := range children {
		if _, ok := ctx.(IModClauseContext); ok {
			len++
		}
	}

	tst := make([]IModClauseContext, len)
	i := 0
	for _, ctx := range children {
		if t, ok := ctx.(IModClauseContext); ok {
			tst[i] = t.(IModClauseContext)
			i++
		}
	}

	return tst
}

func (s *ConjQueryContext) ModClause(i int) IModClauseContext {
	var t antlr.RuleContext
	j := 0
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IModClauseContext); ok {
			if j == i {
				t = ctx.(antlr.RuleContext)
				break
			}
			j++
		}
	}

	if t == nil {
		return nil
	}

	return t.(IModClauseContext)
}

func (s *ConjQueryContext) AllAND() []antlr.TerminalNode {
	return s.GetTokens(LuceneParserAND)
}

func (s *ConjQueryContext) AND(i int) antlr.TerminalNode {
	return s.GetToken(LuceneParserAND, i)
}

func (s *ConjQueryContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *ConjQueryContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *ConjQueryContext) Accept(visitor antlr.ParseTreeVisitor) any {
	switch t := visitor.(type) {
	case LuceneParserVisitor:
		return t.VisitConjQuery(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *LuceneParser) ConjQuery() (localctx IConjQueryContext) {
	localctx = NewConjQueryContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 6, LuceneParserRULE_conjQuery)
	var _la int

	var _alt int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(47)
		p.ModClause()
	}
	p.SetState(52)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_alt = p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 3, p.GetParserRuleContext())
	if p.HasError() {
		goto errorExit
	}
	for _alt != 2 && _alt != antlr.ATNInvalidAltNumber {
		if _alt == 1 {
			{
				p.SetState(48)
				p.Match(LuceneParserAND)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(49)
				p.ModClause()
			}

		}
		p.SetState(54)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_alt = p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 3, p.GetParserRuleContext())
		if p.HasError() {
			goto errorExit
		}
	}
	p.SetState(56)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == LuceneParserAND {
		{
			p.SetState(55)
			p.Match(LuceneParserAND)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IModClauseContext is an interface to support dynamic dispatch.
type IModClauseContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	Clause() IClauseContext
	Modifier() IModifierContext

	// IsModClauseContext differentiates from other interfaces.
	IsModClauseContext()
}

type ModClauseContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyModClauseContext() *ModClauseContext {
	var p = new(ModClauseContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_modClause
	return p
}

func InitEmptyModClauseContext(p *ModClauseContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_modClause
}

func (*ModClauseContext) IsModClauseContext() {}

func NewModClauseContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *ModClauseContext {
	var p = new(ModClauseContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = LuceneParserRULE_modClause

	return p
}

func (s *ModClauseContext) GetParser() antlr.Parser { return s.parser }

func (s *ModClauseContext) Clause() IClauseContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IClauseContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IClauseContext)
}

func (s *ModClauseContext) Modifier() IModifierContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IModifierContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IModifierContext)
}

func (s *ModClauseContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *ModClauseContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *ModClauseContext) Accept(visitor antlr.ParseTreeVisitor) any {
	switch t := visitor.(type) {
	case LuceneParserVisitor:
		return t.VisitModClause(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *LuceneParser) ModClause() (localctx IModClauseContext) {
	localctx = NewModClauseContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 8, LuceneParserRULE_modClause)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	p.SetState(59)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if (int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&104) != 0 {
		{
			p.SetState(58)
			p.Modifier()
		}

	}
	{
		p.SetState(61)
		p.Clause()
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IModifierContext is an interface to support dynamic dispatch.
type IModifierContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	PLUS() antlr.TerminalNode
	MINUS() antlr.TerminalNode
	NOT() antlr.TerminalNode

	// IsModifierContext differentiates from other interfaces.
	IsModifierContext()
}

type ModifierContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyModifierContext() *ModifierContext {
	var p = new(ModifierContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_modifier
	return p
}

func InitEmptyModifierContext(p *ModifierContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_modifier
}

func (*ModifierContext) IsModifierContext() {}

func NewModifierContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *ModifierContext {
	var p = new(ModifierContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = LuceneParserRULE_modifier

	return p
}

func (s *ModifierContext) GetParser() antlr.Parser { return s.parser }

func (s *ModifierContext) PLUS() antlr.TerminalNode {
	return s.GetToken(LuceneParserPLUS, 0)
}

func (s *ModifierContext) MINUS() antlr.TerminalNode {
	return s.GetToken(LuceneParserMINUS, 0)
}

func (s *ModifierContext) NOT() antlr.TerminalNode {
	return s.GetToken(LuceneParserNOT, 0)
}

func (s *ModifierContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *ModifierContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *ModifierContext) Accept(visitor antlr.ParseTreeVisitor) any {
	switch t := visitor.(type) {
	case LuceneParserVisitor:
		return t.VisitModifier(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *LuceneParser) Modifier() (localctx IModifierContext) {
	localctx = NewModifierContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 10, LuceneParserRULE_modifier)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(63)
		_la = p.GetTokenStream().LA(1)

		if !((int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&104) != 0) {
			p.GetErrorHandler().RecoverInline(p)
		} else {
			p.GetErrorHandler().ReportMatch(p)
			p.Consume()
		}
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IClauseContext is an interface to support dynamic dispatch.
type IClauseContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	FieldRangeExpr() IFieldRangeExprContext
	Term() ITermContext
	GroupingExpr() IGroupingExprContext
	FieldName() IFieldNameContext
	OP_COLON() antlr.TerminalNode
	OP_EQUAL() antlr.TerminalNode

	// IsClauseContext differentiates from other interfaces.
	IsClauseContext()
}

type ClauseContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyClauseContext() *ClauseContext {
	var p = new(ClauseContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_clause
	return p
}

func InitEmptyClauseContext(p *ClauseContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_clause
}

func (*ClauseContext) IsClauseContext() {}

func NewClauseContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *ClauseContext {
	var p = new(ClauseContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = LuceneParserRULE_clause

	return p
}

func (s *ClauseContext) GetParser() antlr.Parser { return s.parser }

func (s *ClauseContext) FieldRangeExpr() IFieldRangeExprContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IFieldRangeExprContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IFieldRangeExprContext)
}

func (s *ClauseContext) Term() ITermContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(ITermContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(ITermContext)
}

func (s *ClauseContext) GroupingExpr() IGroupingExprContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IGroupingExprContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IGroupingExprContext)
}

func (s *ClauseContext) FieldName() IFieldNameContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IFieldNameContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IFieldNameContext)
}

func (s *ClauseContext) OP_COLON() antlr.TerminalNode {
	return s.GetToken(LuceneParserOP_COLON, 0)
}

func (s *ClauseContext) OP_EQUAL() antlr.TerminalNode {
	return s.GetToken(LuceneParserOP_EQUAL, 0)
}

func (s *ClauseContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *ClauseContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *ClauseContext) Accept(visitor antlr.ParseTreeVisitor) any {
	switch t := visitor.(type) {
	case LuceneParserVisitor:
		return t.VisitClause(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *LuceneParser) Clause() (localctx IClauseContext) {
	localctx = NewClauseContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 12, LuceneParserRULE_clause)
	var _la int

	p.SetState(75)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}

	switch p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 8, p.GetParserRuleContext()) {
	case 1:
		p.EnterOuterAlt(localctx, 1)
		{
			p.SetState(65)
			p.FieldRangeExpr()
		}

	case 2:
		p.EnterOuterAlt(localctx, 2)
		p.SetState(69)
		p.GetErrorHandler().Sync(p)

		if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 6, p.GetParserRuleContext()) == 1 {
			{
				p.SetState(66)
				p.FieldName()
			}
			{
				p.SetState(67)
				_la = p.GetTokenStream().LA(1)

				if !(_la == LuceneParserOP_COLON || _la == LuceneParserOP_EQUAL) {
					p.GetErrorHandler().RecoverInline(p)
				} else {
					p.GetErrorHandler().ReportMatch(p)
					p.Consume()
				}
			}

		} else if p.HasError() { // JIM
			goto errorExit
		}
		p.SetState(73)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}

		switch p.GetTokenStream().LA(1) {
		case LuceneParserQUOTED, LuceneParserNUMBER, LuceneParserTERM, LuceneParserREGEXPTERM, LuceneParserRANGEIN_START, LuceneParserRANGEEX_START:
			{
				p.SetState(71)
				p.term(0)
			}

		case LuceneParserLPAREN:
			{
				p.SetState(72)
				p.GroupingExpr()
			}

		default:
			p.SetError(antlr.NewNoViableAltException(p, nil, nil, nil, nil, nil))
			goto errorExit
		}

	case antlr.ATNInvalidAltNumber:
		goto errorExit
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IFieldRangeExprContext is an interface to support dynamic dispatch.
type IFieldRangeExprContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	FieldName() IFieldNameContext
	OP_LESSTHAN() antlr.TerminalNode
	OP_MORETHAN() antlr.TerminalNode
	OP_LESSTHANEQ() antlr.TerminalNode
	OP_MORETHANEQ() antlr.TerminalNode
	TERM() antlr.TerminalNode
	QUOTED() antlr.TerminalNode
	NUMBER() antlr.TerminalNode
	OP_COLON() antlr.TerminalNode

	// IsFieldRangeExprContext differentiates from other interfaces.
	IsFieldRangeExprContext()
}

type FieldRangeExprContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyFieldRangeExprContext() *FieldRangeExprContext {
	var p = new(FieldRangeExprContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_fieldRangeExpr
	return p
}

func InitEmptyFieldRangeExprContext(p *FieldRangeExprContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_fieldRangeExpr
}

func (*FieldRangeExprContext) IsFieldRangeExprContext() {}

func NewFieldRangeExprContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *FieldRangeExprContext {
	var p = new(FieldRangeExprContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = LuceneParserRULE_fieldRangeExpr

	return p
}

func (s *FieldRangeExprContext) GetParser() antlr.Parser { return s.parser }

func (s *FieldRangeExprContext) FieldName() IFieldNameContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IFieldNameContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IFieldNameContext)
}

func (s *FieldRangeExprContext) OP_LESSTHAN() antlr.TerminalNode {
	return s.GetToken(LuceneParserOP_LESSTHAN, 0)
}

func (s *FieldRangeExprContext) OP_MORETHAN() antlr.TerminalNode {
	return s.GetToken(LuceneParserOP_MORETHAN, 0)
}

func (s *FieldRangeExprContext) OP_LESSTHANEQ() antlr.TerminalNode {
	return s.GetToken(LuceneParserOP_LESSTHANEQ, 0)
}

func (s *FieldRangeExprContext) OP_MORETHANEQ() antlr.TerminalNode {
	return s.GetToken(LuceneParserOP_MORETHANEQ, 0)
}

func (s *FieldRangeExprContext) TERM() antlr.TerminalNode {
	return s.GetToken(LuceneParserTERM, 0)
}

func (s *FieldRangeExprContext) QUOTED() antlr.TerminalNode {
	return s.GetToken(LuceneParserQUOTED, 0)
}

func (s *FieldRangeExprContext) NUMBER() antlr.TerminalNode {
	return s.GetToken(LuceneParserNUMBER, 0)
}

func (s *FieldRangeExprContext) OP_COLON() antlr.TerminalNode {
	return s.GetToken(LuceneParserOP_COLON, 0)
}

func (s *FieldRangeExprContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *FieldRangeExprContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *FieldRangeExprContext) Accept(visitor antlr.ParseTreeVisitor) any {
	switch t := visitor.(type) {
	case LuceneParserVisitor:
		return t.VisitFieldRangeExpr(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *LuceneParser) FieldRangeExpr() (localctx IFieldRangeExprContext) {
	localctx = NewFieldRangeExprContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 14, LuceneParserRULE_fieldRangeExpr)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(77)
		p.FieldName()
	}
	p.SetState(79)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == LuceneParserOP_COLON {
		{
			p.SetState(78)
			p.Match(LuceneParserOP_COLON)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	}
	{
		p.SetState(81)
		_la = p.GetTokenStream().LA(1)

		if !((int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&30720) != 0) {
			p.GetErrorHandler().RecoverInline(p)
		} else {
			p.GetErrorHandler().ReportMatch(p)
			p.Consume()
		}
	}
	{
		p.SetState(82)
		_la = p.GetTokenStream().LA(1)

		if !((int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&917504) != 0) {
			p.GetErrorHandler().RecoverInline(p)
		} else {
			p.GetErrorHandler().ReportMatch(p)
			p.Consume()
		}
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// ITermContext is an interface to support dynamic dispatch.
type ITermContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	REGEXPTERM() antlr.TerminalNode
	CARAT() antlr.TerminalNode
	AllNUMBER() []antlr.TerminalNode
	NUMBER(i int) antlr.TerminalNode
	TermRangeExpr() ITermRangeExprContext
	QuotedTerm() IQuotedTermContext
	TERM() antlr.TerminalNode
	Term() ITermContext
	Fuzzy() IFuzzyContext

	// IsTermContext differentiates from other interfaces.
	IsTermContext()
}

type TermContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyTermContext() *TermContext {
	var p = new(TermContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_term
	return p
}

func InitEmptyTermContext(p *TermContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_term
}

func (*TermContext) IsTermContext() {}

func NewTermContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *TermContext {
	var p = new(TermContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = LuceneParserRULE_term

	return p
}

func (s *TermContext) GetParser() antlr.Parser { return s.parser }

func (s *TermContext) REGEXPTERM() antlr.TerminalNode {
	return s.GetToken(LuceneParserREGEXPTERM, 0)
}

func (s *TermContext) CARAT() antlr.TerminalNode {
	return s.GetToken(LuceneParserCARAT, 0)
}

func (s *TermContext) AllNUMBER() []antlr.TerminalNode {
	return s.GetTokens(LuceneParserNUMBER)
}

func (s *TermContext) NUMBER(i int) antlr.TerminalNode {
	return s.GetToken(LuceneParserNUMBER, i)
}

func (s *TermContext) TermRangeExpr() ITermRangeExprContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(ITermRangeExprContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(ITermRangeExprContext)
}

func (s *TermContext) QuotedTerm() IQuotedTermContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IQuotedTermContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IQuotedTermContext)
}

func (s *TermContext) TERM() antlr.TerminalNode {
	return s.GetToken(LuceneParserTERM, 0)
}

func (s *TermContext) Term() ITermContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(ITermContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(ITermContext)
}

func (s *TermContext) Fuzzy() IFuzzyContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IFuzzyContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IFuzzyContext)
}

func (s *TermContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *TermContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *TermContext) Accept(visitor antlr.ParseTreeVisitor) any {
	switch t := visitor.(type) {
	case LuceneParserVisitor:
		return t.VisitTerm(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *LuceneParser) Term() (localctx ITermContext) {
	return p.term(0)
}

func (p *LuceneParser) term(_p int) (localctx ITermContext) {
	var _parentctx antlr.ParserRuleContext = p.GetParserRuleContext()

	_parentState := p.GetState()
	localctx = NewTermContext(p, p.GetParserRuleContext(), _parentState)
	var _prevctx ITermContext = localctx
	var _ antlr.ParserRuleContext = _prevctx // TODO: To prevent unused variable warning.
	_startState := 16
	p.EnterRecursionRule(localctx, 16, LuceneParserRULE_term, _p)
	var _alt int

	p.EnterOuterAlt(localctx, 1)
	p.SetState(110)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}

	switch p.GetTokenStream().LA(1) {
	case LuceneParserREGEXPTERM:
		{
			p.SetState(85)
			p.Match(LuceneParserREGEXPTERM)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		p.SetState(88)
		p.GetErrorHandler().Sync(p)

		if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 10, p.GetParserRuleContext()) == 1 {
			{
				p.SetState(86)
				p.Match(LuceneParserCARAT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(87)
				p.Match(LuceneParserNUMBER)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		} else if p.HasError() { // JIM
			goto errorExit
		}

	case LuceneParserRANGEIN_START, LuceneParserRANGEEX_START:
		{
			p.SetState(90)
			p.TermRangeExpr()
		}
		p.SetState(93)
		p.GetErrorHandler().Sync(p)

		if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 11, p.GetParserRuleContext()) == 1 {
			{
				p.SetState(91)
				p.Match(LuceneParserCARAT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(92)
				p.Match(LuceneParserNUMBER)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		} else if p.HasError() { // JIM
			goto errorExit
		}

	case LuceneParserQUOTED:
		{
			p.SetState(95)
			p.QuotedTerm()
		}
		p.SetState(98)
		p.GetErrorHandler().Sync(p)

		if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 12, p.GetParserRuleContext()) == 1 {
			{
				p.SetState(96)
				p.Match(LuceneParserCARAT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(97)
				p.Match(LuceneParserNUMBER)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		} else if p.HasError() { // JIM
			goto errorExit
		}

	case LuceneParserNUMBER:
		{
			p.SetState(100)
			p.Match(LuceneParserNUMBER)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		p.SetState(103)
		p.GetErrorHandler().Sync(p)

		if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 13, p.GetParserRuleContext()) == 1 {
			{
				p.SetState(101)
				p.Match(LuceneParserCARAT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(102)
				p.Match(LuceneParserNUMBER)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		} else if p.HasError() { // JIM
			goto errorExit
		}

	case LuceneParserTERM:
		{
			p.SetState(105)
			p.Match(LuceneParserTERM)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		p.SetState(108)
		p.GetErrorHandler().Sync(p)

		if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 14, p.GetParserRuleContext()) == 1 {
			{
				p.SetState(106)
				p.Match(LuceneParserCARAT)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}
			{
				p.SetState(107)
				p.Match(LuceneParserNUMBER)
				if p.HasError() {
					// Recognition error - abort rule
					goto errorExit
				}
			}

		} else if p.HasError() { // JIM
			goto errorExit
		}

	default:
		p.SetError(antlr.NewNoViableAltException(p, nil, nil, nil, nil, nil))
		goto errorExit
	}
	p.GetParserRuleContext().SetStop(p.GetTokenStream().LT(-1))
	p.SetState(120)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_alt = p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 17, p.GetParserRuleContext())
	if p.HasError() {
		goto errorExit
	}
	for _alt != 2 && _alt != antlr.ATNInvalidAltNumber {
		if _alt == 1 {
			if p.GetParseListeners() != nil {
				p.TriggerExitRuleEvent()
			}
			_prevctx = localctx
			localctx = NewTermContext(p, _parentctx, _parentState)
			p.PushNewRecursionContext(localctx, _startState, LuceneParserRULE_term)
			p.SetState(112)

			if !(p.Precpred(p.GetParserRuleContext(), 6)) {
				p.SetError(antlr.NewFailedPredicateException(p, "p.Precpred(p.GetParserRuleContext(), 6)", ""))
				goto errorExit
			}
			{
				p.SetState(113)
				p.Fuzzy()
			}
			p.SetState(116)
			p.GetErrorHandler().Sync(p)

			if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 16, p.GetParserRuleContext()) == 1 {
				{
					p.SetState(114)
					p.Match(LuceneParserCARAT)
					if p.HasError() {
						// Recognition error - abort rule
						goto errorExit
					}
				}
				{
					p.SetState(115)
					p.Match(LuceneParserNUMBER)
					if p.HasError() {
						// Recognition error - abort rule
						goto errorExit
					}
				}

			} else if p.HasError() { // JIM
				goto errorExit
			}

		}
		p.SetState(122)
		p.GetErrorHandler().Sync(p)
		if p.HasError() {
			goto errorExit
		}
		_alt = p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 17, p.GetParserRuleContext())
		if p.HasError() {
			goto errorExit
		}
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.UnrollRecursionContexts(_parentctx)
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IGroupingExprContext is an interface to support dynamic dispatch.
type IGroupingExprContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	LPAREN() antlr.TerminalNode
	Query() IQueryContext
	RPAREN() antlr.TerminalNode
	CARAT() antlr.TerminalNode
	NUMBER() antlr.TerminalNode

	// IsGroupingExprContext differentiates from other interfaces.
	IsGroupingExprContext()
}

type GroupingExprContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyGroupingExprContext() *GroupingExprContext {
	var p = new(GroupingExprContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_groupingExpr
	return p
}

func InitEmptyGroupingExprContext(p *GroupingExprContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_groupingExpr
}

func (*GroupingExprContext) IsGroupingExprContext() {}

func NewGroupingExprContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *GroupingExprContext {
	var p = new(GroupingExprContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = LuceneParserRULE_groupingExpr

	return p
}

func (s *GroupingExprContext) GetParser() antlr.Parser { return s.parser }

func (s *GroupingExprContext) LPAREN() antlr.TerminalNode {
	return s.GetToken(LuceneParserLPAREN, 0)
}

func (s *GroupingExprContext) Query() IQueryContext {
	var t antlr.RuleContext
	for _, ctx := range s.GetChildren() {
		if _, ok := ctx.(IQueryContext); ok {
			t = ctx.(antlr.RuleContext)
			break
		}
	}

	if t == nil {
		return nil
	}

	return t.(IQueryContext)
}

func (s *GroupingExprContext) RPAREN() antlr.TerminalNode {
	return s.GetToken(LuceneParserRPAREN, 0)
}

func (s *GroupingExprContext) CARAT() antlr.TerminalNode {
	return s.GetToken(LuceneParserCARAT, 0)
}

func (s *GroupingExprContext) NUMBER() antlr.TerminalNode {
	return s.GetToken(LuceneParserNUMBER, 0)
}

func (s *GroupingExprContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *GroupingExprContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *GroupingExprContext) Accept(visitor antlr.ParseTreeVisitor) any {
	switch t := visitor.(type) {
	case LuceneParserVisitor:
		return t.VisitGroupingExpr(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *LuceneParser) GroupingExpr() (localctx IGroupingExprContext) {
	localctx = NewGroupingExprContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 18, LuceneParserRULE_groupingExpr)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(123)
		p.Match(LuceneParserLPAREN)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}
	{
		p.SetState(124)
		p.Query()
	}
	{
		p.SetState(125)
		p.Match(LuceneParserRPAREN)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}
	p.SetState(128)
	p.GetErrorHandler().Sync(p)
	if p.HasError() {
		goto errorExit
	}
	_la = p.GetTokenStream().LA(1)

	if _la == LuceneParserCARAT {
		{
			p.SetState(126)
			p.Match(LuceneParserCARAT)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(127)
			p.Match(LuceneParserNUMBER)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IFieldNameContext is an interface to support dynamic dispatch.
type IFieldNameContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	TERM() antlr.TerminalNode

	// IsFieldNameContext differentiates from other interfaces.
	IsFieldNameContext()
}

type FieldNameContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyFieldNameContext() *FieldNameContext {
	var p = new(FieldNameContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_fieldName
	return p
}

func InitEmptyFieldNameContext(p *FieldNameContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_fieldName
}

func (*FieldNameContext) IsFieldNameContext() {}

func NewFieldNameContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *FieldNameContext {
	var p = new(FieldNameContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = LuceneParserRULE_fieldName

	return p
}

func (s *FieldNameContext) GetParser() antlr.Parser { return s.parser }

func (s *FieldNameContext) TERM() antlr.TerminalNode {
	return s.GetToken(LuceneParserTERM, 0)
}

func (s *FieldNameContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *FieldNameContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *FieldNameContext) Accept(visitor antlr.ParseTreeVisitor) any {
	switch t := visitor.(type) {
	case LuceneParserVisitor:
		return t.VisitFieldName(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *LuceneParser) FieldName() (localctx IFieldNameContext) {
	localctx = NewFieldNameContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 20, LuceneParserRULE_fieldName)
	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(130)
		p.Match(LuceneParserTERM)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// ITermRangeExprContext is an interface to support dynamic dispatch.
type ITermRangeExprContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// GetLeft returns the left token.
	GetLeft() antlr.Token

	// GetRight returns the right token.
	GetRight() antlr.Token

	// SetLeft sets the left token.
	SetLeft(antlr.Token)

	// SetRight sets the right token.
	SetRight(antlr.Token)

	// Getter signatures
	AllRANGE_TO() []antlr.TerminalNode
	RANGE_TO(i int) antlr.TerminalNode
	RANGEIN_START() antlr.TerminalNode
	RANGEEX_START() antlr.TerminalNode
	RANGEIN_END() antlr.TerminalNode
	RANGEEX_END() antlr.TerminalNode
	AllRANGE_GOOP() []antlr.TerminalNode
	RANGE_GOOP(i int) antlr.TerminalNode
	AllRANGE_QUOTED() []antlr.TerminalNode
	RANGE_QUOTED(i int) antlr.TerminalNode

	// IsTermRangeExprContext differentiates from other interfaces.
	IsTermRangeExprContext()
}

type TermRangeExprContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
	left   antlr.Token
	right  antlr.Token
}

func NewEmptyTermRangeExprContext() *TermRangeExprContext {
	var p = new(TermRangeExprContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_termRangeExpr
	return p
}

func InitEmptyTermRangeExprContext(p *TermRangeExprContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_termRangeExpr
}

func (*TermRangeExprContext) IsTermRangeExprContext() {}

func NewTermRangeExprContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *TermRangeExprContext {
	var p = new(TermRangeExprContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = LuceneParserRULE_termRangeExpr

	return p
}

func (s *TermRangeExprContext) GetParser() antlr.Parser { return s.parser }

func (s *TermRangeExprContext) GetLeft() antlr.Token { return s.left }

func (s *TermRangeExprContext) GetRight() antlr.Token { return s.right }

func (s *TermRangeExprContext) SetLeft(v antlr.Token) { s.left = v }

func (s *TermRangeExprContext) SetRight(v antlr.Token) { s.right = v }

func (s *TermRangeExprContext) AllRANGE_TO() []antlr.TerminalNode {
	return s.GetTokens(LuceneParserRANGE_TO)
}

func (s *TermRangeExprContext) RANGE_TO(i int) antlr.TerminalNode {
	return s.GetToken(LuceneParserRANGE_TO, i)
}

func (s *TermRangeExprContext) RANGEIN_START() antlr.TerminalNode {
	return s.GetToken(LuceneParserRANGEIN_START, 0)
}

func (s *TermRangeExprContext) RANGEEX_START() antlr.TerminalNode {
	return s.GetToken(LuceneParserRANGEEX_START, 0)
}

func (s *TermRangeExprContext) RANGEIN_END() antlr.TerminalNode {
	return s.GetToken(LuceneParserRANGEIN_END, 0)
}

func (s *TermRangeExprContext) RANGEEX_END() antlr.TerminalNode {
	return s.GetToken(LuceneParserRANGEEX_END, 0)
}

func (s *TermRangeExprContext) AllRANGE_GOOP() []antlr.TerminalNode {
	return s.GetTokens(LuceneParserRANGE_GOOP)
}

func (s *TermRangeExprContext) RANGE_GOOP(i int) antlr.TerminalNode {
	return s.GetToken(LuceneParserRANGE_GOOP, i)
}

func (s *TermRangeExprContext) AllRANGE_QUOTED() []antlr.TerminalNode {
	return s.GetTokens(LuceneParserRANGE_QUOTED)
}

func (s *TermRangeExprContext) RANGE_QUOTED(i int) antlr.TerminalNode {
	return s.GetToken(LuceneParserRANGE_QUOTED, i)
}

func (s *TermRangeExprContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *TermRangeExprContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *TermRangeExprContext) Accept(visitor antlr.ParseTreeVisitor) any {
	switch t := visitor.(type) {
	case LuceneParserVisitor:
		return t.VisitTermRangeExpr(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *LuceneParser) TermRangeExpr() (localctx ITermRangeExprContext) {
	localctx = NewTermRangeExprContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 22, LuceneParserRULE_termRangeExpr)
	var _la int

	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(132)
		_la = p.GetTokenStream().LA(1)

		if !(_la == LuceneParserRANGEIN_START || _la == LuceneParserRANGEEX_START) {
			p.GetErrorHandler().RecoverInline(p)
		} else {
			p.GetErrorHandler().ReportMatch(p)
			p.Consume()
		}
	}
	{
		p.SetState(133)

		var _lt = p.GetTokenStream().LT(1)

		localctx.(*TermRangeExprContext).left = _lt

		_la = p.GetTokenStream().LA(1)

		if !((int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&7036874417766400) != 0) {
			var _ri = p.GetErrorHandler().RecoverInline(p)

			localctx.(*TermRangeExprContext).left = _ri
		} else {
			p.GetErrorHandler().ReportMatch(p)
			p.Consume()
		}
	}
	{
		p.SetState(134)
		p.Match(LuceneParserRANGE_TO)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}
	{
		p.SetState(135)

		var _lt = p.GetTokenStream().LT(1)

		localctx.(*TermRangeExprContext).right = _lt

		_la = p.GetTokenStream().LA(1)

		if !((int64(_la) & ^0x3f) == 0 && ((int64(1)<<_la)&7036874417766400) != 0) {
			var _ri = p.GetErrorHandler().RecoverInline(p)

			localctx.(*TermRangeExprContext).right = _ri
		} else {
			p.GetErrorHandler().ReportMatch(p)
			p.Consume()
		}
	}
	{
		p.SetState(136)
		_la = p.GetTokenStream().LA(1)

		if !(_la == LuceneParserRANGEIN_END || _la == LuceneParserRANGEEX_END) {
			p.GetErrorHandler().RecoverInline(p)
		} else {
			p.GetErrorHandler().ReportMatch(p)
			p.Consume()
		}
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IQuotedTermContext is an interface to support dynamic dispatch.
type IQuotedTermContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	QUOTED() antlr.TerminalNode
	CARAT() antlr.TerminalNode
	NUMBER() antlr.TerminalNode

	// IsQuotedTermContext differentiates from other interfaces.
	IsQuotedTermContext()
}

type QuotedTermContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyQuotedTermContext() *QuotedTermContext {
	var p = new(QuotedTermContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_quotedTerm
	return p
}

func InitEmptyQuotedTermContext(p *QuotedTermContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_quotedTerm
}

func (*QuotedTermContext) IsQuotedTermContext() {}

func NewQuotedTermContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *QuotedTermContext {
	var p = new(QuotedTermContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = LuceneParserRULE_quotedTerm

	return p
}

func (s *QuotedTermContext) GetParser() antlr.Parser { return s.parser }

func (s *QuotedTermContext) QUOTED() antlr.TerminalNode {
	return s.GetToken(LuceneParserQUOTED, 0)
}

func (s *QuotedTermContext) CARAT() antlr.TerminalNode {
	return s.GetToken(LuceneParserCARAT, 0)
}

func (s *QuotedTermContext) NUMBER() antlr.TerminalNode {
	return s.GetToken(LuceneParserNUMBER, 0)
}

func (s *QuotedTermContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *QuotedTermContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *QuotedTermContext) Accept(visitor antlr.ParseTreeVisitor) any {
	switch t := visitor.(type) {
	case LuceneParserVisitor:
		return t.VisitQuotedTerm(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *LuceneParser) QuotedTerm() (localctx IQuotedTermContext) {
	localctx = NewQuotedTermContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 24, LuceneParserRULE_quotedTerm)
	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(138)
		p.Match(LuceneParserQUOTED)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}
	p.SetState(141)
	p.GetErrorHandler().Sync(p)

	if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 19, p.GetParserRuleContext()) == 1 {
		{
			p.SetState(139)
			p.Match(LuceneParserCARAT)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}
		{
			p.SetState(140)
			p.Match(LuceneParserNUMBER)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	} else if p.HasError() { // JIM
		goto errorExit
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

// IFuzzyContext is an interface to support dynamic dispatch.
type IFuzzyContext interface {
	antlr.ParserRuleContext

	// GetParser returns the parser.
	GetParser() antlr.Parser

	// Getter signatures
	TILDE() antlr.TerminalNode
	NUMBER() antlr.TerminalNode

	// IsFuzzyContext differentiates from other interfaces.
	IsFuzzyContext()
}

type FuzzyContext struct {
	antlr.BaseParserRuleContext
	parser antlr.Parser
}

func NewEmptyFuzzyContext() *FuzzyContext {
	var p = new(FuzzyContext)
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_fuzzy
	return p
}

func InitEmptyFuzzyContext(p *FuzzyContext) {
	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, nil, -1)
	p.RuleIndex = LuceneParserRULE_fuzzy
}

func (*FuzzyContext) IsFuzzyContext() {}

func NewFuzzyContext(parser antlr.Parser, parent antlr.ParserRuleContext, invokingState int) *FuzzyContext {
	var p = new(FuzzyContext)

	antlr.InitBaseParserRuleContext(&p.BaseParserRuleContext, parent, invokingState)

	p.parser = parser
	p.RuleIndex = LuceneParserRULE_fuzzy

	return p
}

func (s *FuzzyContext) GetParser() antlr.Parser { return s.parser }

func (s *FuzzyContext) TILDE() antlr.TerminalNode {
	return s.GetToken(LuceneParserTILDE, 0)
}

func (s *FuzzyContext) NUMBER() antlr.TerminalNode {
	return s.GetToken(LuceneParserNUMBER, 0)
}

func (s *FuzzyContext) GetRuleContext() antlr.RuleContext {
	return s
}

func (s *FuzzyContext) ToStringTree(ruleNames []string, recog antlr.Recognizer) string {
	return antlr.TreesStringTree(s, ruleNames, recog)
}

func (s *FuzzyContext) Accept(visitor antlr.ParseTreeVisitor) any {
	switch t := visitor.(type) {
	case LuceneParserVisitor:
		return t.VisitFuzzy(s)

	default:
		return t.VisitChildren(s)
	}
}

func (p *LuceneParser) Fuzzy() (localctx IFuzzyContext) {
	localctx = NewFuzzyContext(p, p.GetParserRuleContext(), p.GetState())
	p.EnterRule(localctx, 26, LuceneParserRULE_fuzzy)
	p.EnterOuterAlt(localctx, 1)
	{
		p.SetState(143)
		p.Match(LuceneParserTILDE)
		if p.HasError() {
			// Recognition error - abort rule
			goto errorExit
		}
	}
	p.SetState(145)
	p.GetErrorHandler().Sync(p)

	if p.GetInterpreter().AdaptivePredict(p.BaseParser, p.GetTokenStream(), 20, p.GetParserRuleContext()) == 1 {
		{
			p.SetState(144)
			p.Match(LuceneParserNUMBER)
			if p.HasError() {
				// Recognition error - abort rule
				goto errorExit
			}
		}

	} else if p.HasError() { // JIM
		goto errorExit
	}

errorExit:
	if p.HasError() {
		v := p.GetError()
		localctx.SetException(v)
		p.GetErrorHandler().ReportError(p, v)
		p.GetErrorHandler().Recover(p, v)
		p.SetError(nil)
	}
	p.ExitRule()
	return localctx
	goto errorExit // Trick to prevent compiler error if the label is not used
}

func (p *LuceneParser) Sempred(localctx antlr.RuleContext, ruleIndex, predIndex int) bool {
	switch ruleIndex {
	case 8:
		var t *TermContext = nil
		if localctx != nil {
			t = localctx.(*TermContext)
		}
		return p.Term_Sempred(t, predIndex)

	default:
		panic("No predicate with index: " + fmt.Sprint(ruleIndex))
	}
}

func (p *LuceneParser) Term_Sempred(localctx antlr.RuleContext, predIndex int) bool {
	switch predIndex {
	case 0:
		return p.Precpred(p.GetParserRuleContext(), 6)

	default:
		panic("No predicate with index: " + fmt.Sprint(predIndex))
	}
}
