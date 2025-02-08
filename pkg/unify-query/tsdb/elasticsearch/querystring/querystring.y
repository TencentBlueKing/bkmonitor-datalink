%{
package querystring
%}

%union {
	s 	string
	n 	int
	e 	Expr
}

%token tSTRING tPHRASE tNUMBER tSLASH tSTAR
%token tOR tAND tNOT tTO tPLUS tMINUS tCOLON
%token tLEFTBRACKET tRIGHTBRACKET tLEFTRANGE tRIGHTRANGE tLEFTBRACES tRIGHTBRACES
%token tGREATER tLESS tEQUAL

%type <s>                tSTRING
%type <s>                tPHRASE
%type <s>                tNUMBER
%type <s>                tSTAR
%type <s>		 tSLASH
%type <s>                posOrNegNumber
%type <e>                searchBase searchLogicParts searchPart searchLogicPart searchLogicSimplePart
%type <n>                searchPrefix

%left tOR
%left tAND
%nonassoc tLEFTBRACKET tRIGHTBRACKET

%%

input:
searchLogicParts {
	yylex.(*lexerWrapper).expr = $1
};

searchLogicParts:
searchLogicPart searchLogicParts {
	$$ = NewAndExpr($1, $2)
}
|
searchLogicPart {
	$$ = $1
}

searchLogicPart:
searchLogicSimplePart {
	$$ = $1
}
|
searchLogicSimplePart tOR searchLogicPart {
	$$ = NewOrExpr($1, $3)
}
|
searchLogicSimplePart tAND searchLogicPart {
	$$ = NewAndExpr($1, $3)
};

searchLogicSimplePart:
searchPart {
	$$ = $1
}
|
tLEFTBRACKET searchLogicPart tRIGHTBRACKET {
	$$ = $2
}
|
tNOT searchLogicSimplePart {
	$$ = NewNotExpr($2)
};

searchPart:
searchPrefix searchBase {
	switch($1) {
	case queryMustNot:
		$$ = NewNotExpr($2)
	default:
		$$ = $2
	}
}
|
searchBase {
	$$ = $1
};

searchPrefix:
tPLUS {
	$$ = queryMust
}
|
tMINUS {
	$$ = queryMustNot
};

searchBase:
tSTRING {
	$$ = newStringExpr($1)
}
|
tNUMBER {
	$$ = NewMatchExpr($1)
}
|
tPHRASE {
	phrase := $1
	q := NewMatchExpr(phrase)
	$$ = q
}
|
tSLASH{
	phrase := $1
	q := NewRegexpExpr(phrase)
	$$ = q
}
|
tSTRING tCOLON tSTRING {
	q := newStringExpr($3)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tLEFTBRACKET tSTRING tRIGHTBRACKET {
	q := newStringExpr($4)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON posOrNegNumber {
	q := NewMatchExpr($3)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tPHRASE {
	q := NewMatchExpr($3)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tSLASH {
	q := NewRegexpExpr($3)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tGREATER posOrNegNumber {
	val := $4
	q := NewNumberRangeExpr(&val, nil, false, false)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tGREATER tEQUAL posOrNegNumber {
	val := $5
	q := NewNumberRangeExpr(&val, nil, true, false)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tLESS posOrNegNumber {
	val := $4
	q := NewNumberRangeExpr(nil, &val, false, false)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tLESS tEQUAL posOrNegNumber {
	val := $5
	q := NewNumberRangeExpr(nil, &val, false, true)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tGREATER tPHRASE {
	phrase := $4
	q := NewTimeRangeExpr(&phrase, nil, false, false)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tGREATER tEQUAL tPHRASE {
	phrase := $5
	q := NewTimeRangeExpr(&phrase, nil, true, false)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tLESS tPHRASE {
	phrase := $4
	q := NewTimeRangeExpr(nil, &phrase, false, false)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tLESS tEQUAL tPHRASE {
	phrase := $5
	q := NewTimeRangeExpr(nil, &phrase, false, true)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tLEFTRANGE tSTAR tTO posOrNegNumber tRIGHTRANGE {
	max := $6
	q := NewNumberRangeExpr(nil, &max, true, true)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tLEFTRANGE posOrNegNumber tTO tSTAR tRIGHTRANGE {
	min := $4
	q := NewNumberRangeExpr(&min, nil, true, true)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tLEFTRANGE posOrNegNumber tTO posOrNegNumber tRIGHTBRACES {
	min := $4
	max := $6
	q := NewNumberRangeExpr(&min, &max, true, false)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tLEFTRANGE tPHRASE tTO tPHRASE tRIGHTBRACES {
	min := $4
	max := $6
	q := NewTimeRangeExpr(&min, &max, true, false)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tLEFTBRACES posOrNegNumber tTO posOrNegNumber tRIGHTRANGE {
	min := $4
	max := $6
	q := NewNumberRangeExpr(&min, &max, false, true)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tLEFTBRACES tPHRASE tTO tPHRASE tRIGHTRANGE {
	min := $4
	max := $6
	q := NewTimeRangeExpr(&min, &max, false, true)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tLEFTRANGE posOrNegNumber tTO posOrNegNumber tRIGHTRANGE {
	min := $4
	max := $6
	q := NewNumberRangeExpr(&min, &max, true, true)
	q.SetField($1)
	$$ = q
}
|
tSTRING tCOLON tLEFTRANGE tPHRASE tTO tPHRASE tRIGHTRANGE {
	min := $4
	max := $6
	q := NewTimeRangeExpr(&min, &max, true, true)
	q.SetField($1)
	$$ = q
};

posOrNegNumber:
tNUMBER {
	$$ = $1
}
|
tMINUS tNUMBER {
	$$ = "-" + $2
};
