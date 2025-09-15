//  Copyright (c) 2016 Couchbase, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// 		http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package querystring_parser

import (
	"bufio"
	"context"
	"io"
	"strings"
	"unicode"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/unify-query/log"
)

const reservedChars = "+-=&|><!(){}[]^\"~*?:\\/ "

func unescape(escaped string) string {
	// see if this character can be escaped
	if strings.ContainsAny(escaped, reservedChars) {
		return escaped
	}
	// otherwise return it with the \ intact
	return "\\" + escaped
}

type queryStringLex struct {
	in            *bufio.Reader
	buf           string
	currState     lexState
	currConsumed  bool
	inEscape      bool
	nextToken     *yySymType
	nextTokenType int
	seenDot       bool
	nextRune      rune
	nextRuneSize  int
	atEOF         bool
}

func (l *queryStringLex) reset() {
	l.buf = ""
	l.inEscape = false
	l.seenDot = false
}

func (l *queryStringLex) Error(msg string) {
	log.Errorf(context.TODO(), msg)
}

func (l *queryStringLex) Lex(lval *yySymType) (rv int) {
	var err error

	for l.nextToken == nil {
		if l.currConsumed {
			l.nextRune, l.nextRuneSize, err = l.in.ReadRune()
			if err != nil && err == io.EOF {
				l.nextRune = 0
				l.atEOF = true
			} else if err != nil {
				return 0
			}
		}

		l.currState, l.currConsumed = l.currState(l, l.nextRune, l.atEOF)
		if l.currState == nil {
			return 0
		}
	}

	*lval = *l.nextToken
	rv = l.nextTokenType
	l.nextToken = nil
	l.nextTokenType = 0
	return rv
}

func newExprStringLex(in io.Reader) *queryStringLex {
	return &queryStringLex{
		in:           bufio.NewReader(in),
		currState:    startState,
		currConsumed: true,
	}
}

type lexState func(l *queryStringLex, next rune, eof bool) (lexState, bool)

func startState(l *queryStringLex, next rune, eof bool) (lexState, bool) {
	if eof {
		return nil, false
	}

	// handle inside escape case up front
	if l.inEscape {
		l.inEscape = false
		l.buf += unescape(string(next))
		return inStrState, true
	}

	switch next {
	case '"':
		return inPhraseState, true
	case '/':
		// 检查下一个字符是否是EOF或空格，如果是则返回tSLASH
		peekRune, _, err := l.in.ReadRune()
		if err != nil {
			if err == io.EOF {
				l.nextTokenType = tSLASH
				l.nextToken = &yySymType{s: "/"}
				l.reset()
				return startState, true
			}
			return nil, false
		}
		// 回退读取的字符
		l.in.UnreadRune()

		// 如果是单独的/符号
		if unicode.IsSpace(peekRune) {
			l.nextTokenType = tSLASH
			l.nextToken = &yySymType{s: "/"}
			l.reset()
			return startState, true
		}
		// 否则进入正则表达式状态
		return inRegexState, true
	case '+', '-', ':', '>', '<', '=', '(', ')', '[', ']', '{', '}':
		l.buf += string(next)
		return singleCharOpState, true
	}

	switch {
	case !l.inEscape && next == '\\':
		l.inEscape = true
		return startState, true
	case unicode.IsDigit(next):
		l.buf += string(next)
		return inNumOrStrState, true
	case !unicode.IsSpace(next):
		l.buf += string(next)
		return inStrState, true
	}

	// doesn't look like anything, just eat it and stay here
	l.reset()
	return startState, true
}

func inPhraseState(l *queryStringLex, next rune, eof bool) (lexState, bool) {
	// unterminated phrase eats the phrase
	if eof {
		l.Error("unterminated quote")
		return nil, false
	}

	// only a non-escaped " ends the phrase
	if !l.inEscape && next == '"' {
		l.nextTokenType = tPHRASE
		l.nextToken = &yySymType{
			s: l.buf,
		}
		// log.Debugf(context.TODO(), "PHRASE - '%s'", l.nextToken.s)
		l.reset()
		return startState, true
	} else if !l.inEscape && next == '\\' {
		l.inEscape = true
	} else if l.inEscape {
		// if in escape, end it
		l.inEscape = false
		l.buf += unescape(string(next))
	} else {
		l.buf += string(next)
	}

	return inPhraseState, true
}

func singleCharOpState(l *queryStringLex, next rune, eof bool) (lexState, bool) {
	l.nextToken = &yySymType{}

	switch l.buf {
	case "+":
		l.nextTokenType = tPLUS
	case "-":
		l.nextTokenType = tMINUS
	case ":":
		l.nextTokenType = tCOLON
	case ">":
		l.nextTokenType = tGREATER
	case "<":
		l.nextTokenType = tLESS
	case "=":
		l.nextTokenType = tEQUAL
	case "(":
		l.nextTokenType = tLEFTBRACKET
	case ")":
		l.nextTokenType = tRIGHTBRACKET
	case "[":
		l.nextTokenType = tLEFTRANGE
	case "]":
		l.nextTokenType = tRIGHTRANGE
	case "{":
		l.nextTokenType = tLEFTBRACES
	case "}":
		l.nextTokenType = tRIGHTBRACES
	}

	l.reset()
	return startState, false
}

func inRegexState(l *queryStringLex, next rune, eof bool) (lexState, bool) {
	if eof {
		l.Error("unterminated regex")
		return nil, false
	}

	// 非转义的/结束正则表达式
	if !l.inEscape && next == '/' {
		l.nextTokenType = tREGEX
		l.nextToken = &yySymType{s: l.buf}
		l.reset()
		return startState, true
	} else if !l.inEscape && next == '\\' {
		l.inEscape = true
	} else if l.inEscape {
		l.inEscape = false
		l.buf += unescape(string(next))
	} else {
		l.buf += string(next)
	}

	return inRegexState, true
}

func inNumOrStrState(l *queryStringLex, next rune, eof bool) (lexState, bool) {
	// only a non-escaped space ends the tilde (or eof)
	if eof || (!l.inEscape && next == ' ' || next == ':' || next == ')' || next == ']' || next == '}') {
		// end number
		consumed := true
		if !eof && (next == ':' || next == ')' || next == ']' || next == '}') {
			consumed = false
		}

		l.nextTokenType = tNUMBER
		l.nextToken = &yySymType{
			s: l.buf,
		}
		// log.Debugf(context.TODO(), "NUMBER - '%s'", l.nextToken.s)
		l.reset()
		return startState, consumed
	} else if !l.inEscape && next == '\\' {
		l.inEscape = true
		return inNumOrStrState, true
	} else if l.inEscape {
		// if in escape, end it
		l.inEscape = false
		l.buf += unescape(string(next))
		// go directly to string, no successfully or unsuccessfully
		// escaped string results in a valid number
		return inStrState, true
	}

	// see where to go
	if !l.seenDot && next == '.' {
		// stay in this state
		l.seenDot = true
		l.buf += string(next)
		return inNumOrStrState, true
	} else if unicode.IsDigit(next) {
		l.buf += string(next)
		return inNumOrStrState, true
	}

	// doesn't look like an number, transition
	l.buf += string(next)
	return inStrState, true
}

func inStrState(l *queryStringLex, next rune, eof bool) (lexState, bool) {
	// end on non-escped space, colon, tilde, boost (or eof)
	if eof || (!l.inEscape && (next == ' ' || next == ':' || next == ')' || next == ']' || next == '}')) {
		// end string
		consumed := true
		if !eof && (next == ':' || next == ')' || next == ']' || next == '}') {
			consumed = false
		}

		switch strings.ToLower(l.buf) {
		case "and":
			l.nextTokenType = tAND
			l.nextToken = &yySymType{}
			l.reset()
			return startState, consumed
		case "or":
			l.nextTokenType = tOR
			l.nextToken = &yySymType{}
			l.reset()
			return startState, consumed
		case "not":
			l.nextTokenType = tNOT
			l.nextToken = &yySymType{}
			l.reset()
			return startState, consumed
		case "to":
			l.nextTokenType = tTO
			l.nextToken = &yySymType{}
			l.reset()
			return startState, consumed
		}

		l.nextTokenType = tSTRING
		l.nextToken = &yySymType{
			s: l.buf,
		}
		// log.Debugf(context.TODO(), "STRING - '%s'", l.nextToken.s)
		l.reset()

		return startState, consumed
	} else if !l.inEscape && next == '\\' {
		l.inEscape = true
	} else if l.inEscape {
		// if in escape, end it
		l.inEscape = false
		l.buf += unescape(string(next))
	} else {
		l.buf += string(next)
	}

	return inStrState, true
}
