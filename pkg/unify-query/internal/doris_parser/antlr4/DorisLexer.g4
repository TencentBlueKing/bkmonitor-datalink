// Licensed to the Apache Software Foundation (ASF) under one
// or more contributor license agreements.  See the NOTICE file
// distributed with this work for additional information
// regarding copyright ownership.  The ASF licenses this file
// to you under the Apache License, Version 2.0 (the
// "License"); you may not use this file except in compliance
// with the License.  You may obtain a copy of the License at
//
//   http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing,
// software distributed under the License is distributed on an
// "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
// KIND, either express or implied.  See the License for the
// specific language governing permissions and limitations
// under the License.

// Copied from Apache Spark and modified for Apache Doris

lexer grammar DorisLexer;


@members {
    var has_unclosed_bracketed_comment = false;

    func isValidDecimal(ctx antlr.RuleContext) bool {
    	fmt.Println("isValidDecimal input:", ctx.GetText())
    	return true
    }

    func markUnclosedComment() {
        has_unclosed_bracketed_comment = true;
    }
}

fragment A : [aA];
fragment B : [bB];
fragment C : [cC];
fragment D : [dD];
fragment E : [eE];
fragment F : [fF];
fragment G : [gG];
fragment H : [hH];
fragment I : [iI];
fragment J : [jJ];
fragment K : [kK];
fragment L : [lL];
fragment M : [mM];
fragment N : [nN];
fragment O : [oO];
fragment P : [pP];
fragment Q : [qQ];
fragment R : [rR];
fragment S : [sS];
fragment T : [tT];
fragment U : [uU];
fragment V : [vV];
fragment W : [wW];
fragment X : [xX];
fragment Y : [yY];
fragment Z : [zZ];

SEMICOLON: ';';

LEFT_PAREN: '(';
RIGHT_PAREN: ')';
COMMA: ',';
DOT: '.';
DOTDOTDOT: '...';
LEFT_BRACKET: '[';
RIGHT_BRACKET: ']';
LEFT_BRACE: '{';
RIGHT_BRACE: '}';

// TODO: add a doc to list reserved words

//============================
// Start of the keywords list
//============================
//--DORIS-KEYWORD-LIST-START
ACCOUNT_LOCK: A C C O U N T '_' L O C K ;
ACCOUNT_UNLOCK: A C C O U N T '_' U N L O C K ;
ACTIONS: A C T I O N S ;
ADD: A D D ;
ADMIN: A D M I N ;
AFTER: A F T E R ;
AGG_STATE: A G G '_' S T A T E ;
AGGREGATE: A G G R E G A T E ;
ALIAS: A L I A S ;
ALL: A L L ;
ALTER: A L T E R ;
ANALYZE: A N A L Y Z E ;
ANALYZED: A N A L Y Z E D ;
ANALYZER: A N A L Y Z E R ;
AND: A N D ;
ANTI: A N T I ;
APPEND: A P P E N D ;
ARRAY: A R R A Y ;
AS: A S ;
ASC: A S C ;
AT: A T ;
AUTHORS: A U T H O R S ;
AUTO: A U T O ;
AUTO_INCREMENT: A U T O '_' I N C R E M E N T ;
ALWAYS: A L W A Y S ;
BACKEND: B A C K E N D ;
BACKENDS: B A C K E N D S ;
BACKUP: B A C K U P ;
BEGIN: B E G I N ;
BELONG: B E L O N G ;
BETWEEN: B E T W E E N ;
BIGINT: B I G I N T ;
BIN: B I N ;
BINARY: B I N A R Y ;
BINLOG: B I N L O G ;
BITAND: B I T A N D ;
BITMAP: B I T M A P ;
BITMAP_EMPTY: B I T M A P '_' E M P T Y ;
BITMAP_UNION: B I T M A P '_' U N I O N ;
BITOR: B I T O R ;
BITXOR: B I T X O R ;
BLOB: B L O B ;
BOOLEAN: B O O L E A N ;
BRANCH: B R A N C H ;
BRIEF: B R I E F ;
BROKER: B R O K E R ;
BUCKETS: B U C K E T S ;
BUILD: B U I L D ;
BUILTIN: B U I L T I N ;
BULK: B U L K ;
BY: B Y ;
CACHE: C A C H E ;
CACHED: C A C H E D ;
CALL: C A L L ;
CANCEL: C A N C E L ;
CASE: C A S E ;
CAST: C A S T ;
CATALOG: C A T A L O G ;
CATALOGS: C A T A L O G S ;
CHAIN: C H A I N ;
CHAR: C H A R ;
CHARSET: C H A R S E T ;
CHECK: C H E C K ;
CLEAN: C L E A N ;
CLUSTER: C L U S T E R ;
CLUSTERS: C L U S T E R S ;
COLLATE: C O L L A T E ;
COLLATION: C O L L A T I O N ;
COLLECT: C O L L E C T ;
COLOCATE: C O L O C A T E ;
COLUMN: C O L U M N ;
COLUMNS: C O L U M N S ;
COMMENT: C O M M E N T ;
COMMIT: C O M M I T ;
COMMITTED: C O M M I T T E D ;
COMPACT: C O M P A C T ;
COMPLETE: C O M P L E T E ;
COMPRESS_TYPE: C O M P R E S S '_' T Y P E ;
COMPUTE: C O M P U T E ;
CONDITIONS: C O N D I T I O N S ;
CONFIG: C O N F I G ;
CONNECTION: C O N N E C T I O N ;
CONNECTION_ID: C O N N E C T I O N '_' I D ;
CONSISTENT: C O N S I S T E N T ;
CONSTRAINT: C O N S T R A I N T ;
CONSTRAINTS: C O N S T R A I N T S ;
CONVERT: C O N V E R T ;
CONVERT_LSC: C O N V E R T '_' L S C ;
COPY: C O P Y ;
COUNT: C O U N T ;
CREATE: C R E A T E ;
CREATION: C R E A T I O N ;
CRON: C R O N ;
CROSS: C R O S S ;
CUBE: C U B E ;
CURRENT: C U R R E N T ;
CURRENT_CATALOG: C U R R E N T '_' C A T A L O G ;
CURRENT_DATE: C U R R E N T '_' D A T E ;
CURRENT_TIME: C U R R E N T '_' T I M E ;
CURRENT_TIMESTAMP: C U R R E N T '_' T I M E S T A M P ;
CURRENT_USER: C U R R E N T '_' U S E R ;
DATA: D A T A ;
DATABASE: D A T A B A S E ;
DATABASES: D A T A B A S E S ;
DATE: D A T E ;
DATETIME: D A T E T I M E ;
DATETIMEV2: D A T E T I M E V '2';
DATEV2: D A T E V '2';
DATETIMEV1: D A T E T I M E V  '1';
DATEV1: D A T E V '1';
DAY: D A Y ;
DAYS: D A Y S ;
DECIMAL: D E C I M A L ;
DECIMALV2: D E C I M A L V '2';
DECIMALV3: D E C I M A L V '3';
DECOMMISSION: D E C O M M I S S I O N ;
DEFAULT: D E F A U L T ;
DEFERRED: D E F E R R E D ;
DELETE: D E L E T E ;
DEMAND: D E M A N D ;
DESC: D E S C ;
DESCRIBE: D E S C R I B E ;
DIAGNOSE: D I A G N O S E ;
DIAGNOSIS: D I A G N O S I S ;
DICTIONARIES: D I C T I O N A R I E S ;
DICTIONARY: D I C T I O N A R Y ;
DISK: D I S K ;
DISTINCT: D I S T I N C T ;
DISTINCTPC: D I S T I N C T P C ;
DISTINCTPCSA: D I S T I N C T P C S A ;
DISTRIBUTED: D I S T R I B U T E D ;
DISTRIBUTION: D I S T R I B U T I O N ;
DIV: D I V ;
DO: D O ;
DORIS_INTERNAL_TABLE_ID: D O R I S '_' I N T E R N A L '_' T A B L E '_' I D ;
DOUBLE: D O U B L E ;
DROP: D R O P ;
DROPP: D R O P P ;
DUAL: D U A L ;
DUMP: D U M P ;
DUPLICATE: D U P L I C A T E ;
DYNAMIC: D Y N A M I C ;
ELSE: E L S E ;
ENABLE: E N A B L E ;
ENCRYPTKEY: E N C R Y P T K E Y ;
ENCRYPTKEYS: E N C R Y P T K E Y S ;
END: E N D ;
ENDS: E N D S ;
ENGINE: E N G I N E ;
ENGINES: E N G I N E S ;
ENTER: E N T E R ;
ERRORS: E R R O R S ;
ESCAPE: E S C A P E ;
EVENTS: E V E N T S ;
EVERY: E V E R Y ;
EXCEPT: E X C E P T ;
EXCLUDE: E X C L U D E ;
EXECUTE: E X E C U T E ;
EXISTS: E X I S T S ;
EXPIRED: E X P I R E D ;
EXPLAIN: E X P L A I N ;
EXPORT: E X P O R T ;
EXTENDED: E X T E N D E D ;
EXTERNAL: E X T E R N A L ;
EXTRACT: E X T R A C T ;
FAILED_LOGIN_ATTEMPTS: F A I L E D '_' L O G I N '_' A T T E M P T S ;
FALSE: F A L S E ;
FAST: F A S T ;
FEATURE: F E A T U R E ;
FIELDS: F I E L D S ;
FILE: F I L E ;
FILTER: F I L T E R ;
FIRST: F I R S T ;
FLOAT: F L O A T ;
FOLLOWER: F O L L O W E R ;
FOLLOWING: F O L L O W I N G ;
FOR: F O R ;
FOREIGN: F O R E I G N ;
FORCE: F O R C E ;
FORMAT: F O R M A T ;
FREE: F R E E ;
FROM: F R O M ;
FRONTEND: F R O N T E N D ;
FRONTENDS: F R O N T E N D S ;
FULL: F U L L ;
FUNCTION: F U N C T I O N ;
FUNCTIONS: F U N C T I O N S ;
GENERATED: G E N E R A T E D ;
GENERIC: G E N E R I C ;
GLOBAL: G L O B A L ;
GRANT: G R A N T ;
GRANTS: G R A N T S ;
GRAPH: G R A P H ;
GROUP: G R O U P ;
GROUPING: G R O U P I N G ;
GROUPS: G R O U P S ;
HASH: H A S H ;
HASH_MAP: H A S H '_' M A P ;
HAVING: H A V I N G ;
HDFS: H D F S ;
HELP: H E L P ;
HISTOGRAM: H I S T O G R A M ;
HLL: H L L ;
HLL_UNION: H L L '_' U N I O N ;
HOSTNAME: H O S T N A M E ;
HOTSPOT: H O T S P O T ;
HOUR: H O U R ;
HOURS: H O U R S ;
HUB: H U B ;
IDENTIFIED: I D E N T I F I E D ;
IF: I F ;
IGNORE: I G N O R E ;
IMMEDIATE: I M M E D I A T E ;
IN: I N ;
INCREMENTAL: I N C R E M E N T A L ;
INDEX: I N D E X ;
INDEXES: I N D E X E S ;
INFILE: I N F I L E ;
INNER: I N N E R ;
INSERT: I N S E R T ;
INSTALL: I N S T A L L ;
INT: I N T ;
INTEGER: I N T E G E R ;
INTERMEDIATE: I N T E R M E D I A T E ;
INTERSECT: I N T E R S E C T ;
INTERVAL: I N T E R V A L ;
INTO: I N T O ;
INVERTED: I N V E R T E D ;
IP_TRIE: I P '_' T R I E ;
IPV4: I P V '4';
IPV6: I P V '6';
IS: I S ;
IS_NOT_NULL_PRED: I S '_' N O T '_' N U L L '_' P R E D ;
IS_NULL_PRED: I S '_' N U L L '_' P R E D ;
ISNULL: I S N U L L ;
ISOLATION: I S O L A T I O N ;
JOB: J O B ;
JOBS: J O B S ;
JOIN: J O I N ;
JSON: J S O N ;
JSONB: J S O N B ;
KEY: K E Y ;
KEYS: K E Y S ;
KILL: K I L L ;
LABEL: L A B E L ;
LARGEINT: L A R G E I N T ;
LAYOUT: L A Y O U T ;
LAST: L A S T ;
LATERAL: L A T E R A L ;
LDAP: L D A P ;
LDAP_ADMIN_PASSWORD: L D A P '_' A D M I N '_' P A S S W O R D ;
LEFT: L E F T ;
LESS: L E S S ;
LEVEL: L E V E L ;
LIKE: L I K E ;
LIMIT: L I M I T ;
LINES: L I N E S ;
LINK: L I N K ;
LIST: L I S T ;
LOAD: L O A D ;
LOCAL: L O C A L ;
LOCALTIME: L O C A L T I M E ;
LOCALTIMESTAMP: L O C A L T I M E S T A M P ;
LOCATION: L O C A T I O N ;
LOCK: L O C K ;
LOGICAL: L O G I C A L ;
LOW_PRIORITY: L O W '_' P R I O R I T Y ;
MANUAL: M A N U A L ;
MAP: M A P ;
MATCH: M A T C H ;
MATCH_ALL: M A T C H '_' A L L ;
MATCH_ANY: M A T C H '_' A N Y ;
MATCH_PHRASE: M A T C H '_' P H R A S E ;
MATCH_PHRASE_EDGE: M A T C H '_' P H R A S E '_' E D G E ;
MATCH_PHRASE_PREFIX: M A T C H '_' P H R A S E '_' P R E F I X ;
MATCH_REGEXP: M A T C H '_' R E G E X P ;
MATERIALIZED: M A T E R I A L I Z E D ;
MAX: M A X ;
MAXVALUE: M A X V A L U E ;
MEMO: M E M O ;
MERGE: M E R G E ;
MIGRATE: M I G R A T E ;
MIGRATIONS: M I G R A T I O N S ;
MIN: M I N ;
MINUS: M I N U S ;
MINUTE: M I N U T E ;
MINUTES: M I N U T E S ;
MODIFY: M O D I F Y ;
MONTH: M O N T H ;
MTMV: M T M V ;
NAME: N A M E ;
NAMES: N A M E S ;
NATURAL: N A T U R A L ;
NEGATIVE: N E G A T I V E ;
NEVER: N E V E R ;
NEXT: N E X T ;
NGRAM_BF: N G R A M '_' B F ;
NO: N O ;
NO_USE_MV: N O '_' U S E '_' M V ;
NON_NULLABLE: N O N '_' N U L L A B L E ;
NOT: N O T ;
NULL: N U L L ;
NULLS: N U L L S ;
OBSERVER: O B S E R V E R ;
OF: O F ;
OFFSET: O F F S E T ;
ON: O N ;
ONLY: O N L Y ;
OPEN: O P E N ;
OPTIMIZED: O P T I M I Z E D ;
OR: O R ;
ORDER: O R D E R ;
OUTER: O U T E R ;
OUTFILE: O U T F I L E ;
OVER: O V E R ;
OVERWRITE: O V E R W R I T E ;
PARAMETER: P A R A M E T E R ;
PARSED: P A R S E D ;
PARTITION: P A R T I T I O N ;
PARTITIONS: P A R T I T I O N S ;
PASSWORD: P A S S W O R D ;
PASSWORD_EXPIRE: P A S S W O R D '_' E X P I R E ;
PASSWORD_HISTORY: P A S S W O R D '_' H I S T O R Y ;
PASSWORD_LOCK_TIME: P A S S W O R D '_' L O C K '_' T I M E ;
PASSWORD_REUSE: P A S S W O R D '_' R E U S E ;
PATH: P A T H ;
PAUSE: P A U S E ;
PERCENT: P E R C E N T ;
PERIOD: P E R I O D ;
PERMISSIVE: P E R M I S S I V E ;
PHYSICAL: P H Y S I C A L ;
PI: P I ;
PLACEHOLDER: P L A C E H O L D E R ;
PLAN: P L A N ;
PLAY: P L A Y ;
PRIVILEGES: P R I V I L E G E S ;
PROCESS: P R O C E S S ;
PLUGIN: P L U G I N ;
PLUGINS: P L U G I N S ;
POLICY: P O L I C Y ;
PRECEDING: P R E C E D I N G ;
PREPARE: P R E P A R E ;
PRIMARY: P R I M A R Y ;
PROC: P R O C ;
PROCEDURE: P R O C E D U R E ;
PROCESSLIST: P R O C E S S L I S T ;
PROFILE: P R O F I L E ;
PROPERTIES: P R O P E R T I E S ;
PROPERTY: P R O P E R T Y ;
QUANTILE_STATE: Q U A N T I L E '_' S T A T E ;
QUANTILE_UNION: Q U A N T I L E '_' U N I O N ;
QUERY: Q U E R Y ;
QUEUED: Q U E U E D ;
QUOTA: Q U O T A ;
QUALIFY: Q U A L I F Y ;
QUARTER: Q U A R T E R ;
RANDOM: R A N D O M ;
RANGE: R A N G E ;
READ: R E A D ;
REAL: R E A L ;
REBALANCE: R E B A L A N C E ;
RECENT: R E C E N T ;
RECOVER: R E C O V E R ;
RECYCLE: R E C Y C L E ;
REFRESH: R E F R E S H ;
REFERENCES: R E F E R E N C E S ;
REGEXP: R E G E X P ;
RELEASE: R E L E A S E ;
RENAME: R E N A M E ;
REPAIR: R E P A I R ;
REPEATABLE: R E P E A T A B L E ;
REPLACE: R E P L A C E ;
REPLACE_IF_NOT_NULL: R E P L A C E '_' I F '_' N O T '_' N U L L ;
REPLAYER: R E P L A Y E R ;
REPLICA: R E P L I C A ;
REPOSITORIES: R E P O S I T O R I E S ;
REPOSITORY: R E P O S I T O R Y ;
RESOURCE: R E S O U R C E ;
RESOURCES: R E S O U R C E S ;
RESTORE: R E S T O R E ;
RESTRICTIVE: R E S T R I C T I V E ;
RESUME: R E S U M E ;
RETAIN: R E T A I N ;
RETENTION: R E T E N T I O N ;
RETURNS: R E T U R N S ;
REVOKE: R E V O K E ;
REWRITTEN: R E W R I T T E N ;
RIGHT: R I G H T ;
RLIKE: R L I K E ;
ROLE: R O L E ;
ROLES: R O L E S ;
ROLLBACK: R O L L B A C K ;
ROLLUP: R O L L U P ;
ROUTINE: R O U T I N E ;
ROW: R O W ;
ROWS: R O W S ;
S3: S '3';
SAMPLE: S A M P L E ;
SCHEDULE: S C H E D U L E ;
SCHEDULER: S C H E D U L E R ;
SCHEMA: S C H E M A ;
SCHEMAS: S C H E M A S ;
SECOND: S E C O N D ;
SELECT: S E L E C T ;
SEMI: S E M I ;
SERIALIZABLE: S E R I A L I Z A B L E ;
SESSION: S E S S I O N ;
SESSION_USER: S E S S I O N '_' U S E R ;
SET: S E T ;
SETS: S E T S ;
SET_SESSION_VARIABLE: S E T '_' S E S S I O N '_' V A R I A B L E ;
SHAPE: S H A P E ;
SHOW: S H O W ;
SIGNED: S I G N E D ;
SKEW: S K E W ;
SMALLINT: S M A L L I N T ;
SNAPSHOT: S N A P S H O T ;
SNAPSHOTS: S N A P S H O T S ;
SONAME: S O N A M E ;
SPLIT: S P L I T ;
SQL: S Q L ;
SQL_BLOCK_RULE: S Q L '_' B L O C K '_' R U L E ;
STAGE: S T A G E ;
STAGES: S T A G E S ;
START: S T A R T ;
STARTS: S T A R T S ;
STATS: S T A T S ;
STATUS: S T A T U S ;
STOP: S T O P ;
STORAGE: S T O R A G E ;
STREAM: S T R E A M ;
STREAMING: S T R E A M I N G ;
STRING: S T R I N G ;
STRUCT: S T R U C T ;
SUM: S U M ;
SUPERUSER: S U P E R U S E R ;
SWITCH: S W I T C H ;
SYNC: S Y N C ;
SYSTEM: S Y S T E M ;
TABLE: T A B L E ;
TABLES: T A B L E S ;
TABLESAMPLE: T A B L E S A M P L E ;
TABLET: T A B L E T ;
TABLETS: T A B L E T S ;
TAG: T A G ;
TASK: T A S K ;
TASKS: T A S K S ;
TEMPORARY: T E M P O R A R Y ;
TERMINATED: T E R M I N A T E D ;
TEXT: T E X T ;
THAN: T H A N ;
THEN: T H E N ;
TIME: T I M E ;
TIMESTAMP: T I M E S T A M P ;
TINYINT: T I N Y I N T ;
TO: T O ;
TOKENIZER: T O K E N I Z E R ;
TOKEN_FILTER: T O K E N '_' F I L T E R ;
TRANSACTION: T R A N S A C T I O N ;
TRASH: T R A S H ;
TREE: T R E E ;
TRIGGERS: T R I G G E R S ;
TRIM: T R I M ;
TRUE: T R U E ;
TRUNCATE: T R U N C A T E ;
TYPE: T Y P E ;
TYPECAST: T Y P E C A S T ;
TYPES: T Y P E S ;
UNBOUNDED: U N B O U N D E D ;
UNCOMMITTED: U N C O M M I T T E D ;
UNINSTALL: U N I N S T A L L ;
UNION: U N I O N ;
UNIQUE: U N I Q U E ;
UNLOCK: U N L O C K ;
UNSET: U N S E T ;
UNSIGNED: U N S I G N E D ;
UP: U P ;
UPDATE: U P D A T E ;
USE: U S E ;
USER: U S E R ;
USE_MV: U S E '_' M V ;
USING: U S I N G ;
VALUE: V A L U E ;
VALUES: V A L U E S ;
VARCHAR: V A R C H A R ;
VARIABLE: V A R I A B L E ;
VARIABLES: V A R I A B L E S ;
VARIANT: V A R I A N T ;
VAULT: V A U L T ;
VAULTS: V A U L T S ;
VERBOSE: V E R B O S E ;
VERSION: V E R S I O N ;
VIEW: V I E W ;
VIEWS: V I E W S ;
WARM: W A R M ;
WARNINGS: W A R N I N G S ;
WEEK: W E E K ;
WHEN: W H E N ;
WHERE: W H E R E ;
WHITELIST: W H I T E L I S T ;
WITH: W I T H ;
WORK: W O R K ;
WORKLOAD: W O R K L O A D ;
WRITE: W R I T E ;
XOR: X O R ;
YEAR: Y E A R ;
//--DORIS-KEYWORD-LIST-END
//============================
// End of the keywords list
//============================

EQ  : '=' | '==';
NSEQ: '<=>';
NEQ : '<>' | '!=';
LT  : '<';
LTE : '<=' | '!>';
GT  : '>';
GTE : '>=' | '!<';

PLUS: '+';
SUBTRACT: '-';
ASTERISK: '*';
SLASH: '/';
MOD: '%';
TILDE: '~';
AMPERSAND: '&';
LOGICALAND: '&&';
LOGICALNOT: '!';
PIPE: '|';
DOUBLEPIPES: '||';
HAT: '^';
COLON: ':';
ARROW: '->';
HINT_START: '/*+';
HINT_END: '*/';
COMMENT_START: '/*';
ATSIGN: '@';
DOUBLEATSIGN: '@@';

STRING_LITERAL
    : '\'' ('\\'. | '\'\'' | ~('\'' | '\\'))* '\''
    | '"' ( '\\'. | '""' | ~('"'| '\\') )* '"'
    | 'R\'' (~'\'')* '\''
    | 'R"'(~'"')* '"'
    ;

LEADING_STRING
    : LEFT_BRACE
    | RIGHT_BRACE
    | LEFT_BRACKET
    | RIGHT_BRACKET
    ;

BIGINT_LITERAL
    : DIGIT+ L
    ;

SMALLINT_LITERAL
    : DIGIT+ S
    ;

TINYINT_LITERAL
    : DIGIT+ Y
    ;

INTEGER_VALUE
    : DIGIT+
    ;

EXPONENT_VALUE
    : DIGIT+ EXPONENT
    | DECIMAL_DIGITS EXPONENT {isValidDecimal(localctx)}?
    ;

DECIMAL_VALUE
    : DECIMAL_DIGITS {isValidDecimal(localctx)}?
    ;

BIGDECIMAL_LITERAL
    : DIGIT+ EXPONENT? B D
    | DECIMAL_DIGITS EXPONENT? B D {isValidDecimal(localctx)}?
    ;

IDENTIFIER
    : (LETTER | DIGIT | '_')+
    ;

BACKQUOTED_IDENTIFIER
    : '`' ( ~'`' | '``' )* '`'
    ;

fragment DECIMAL_DIGITS
    : DIGIT+ '.' DIGIT*
    | '.' DIGIT+
    ;

fragment EXPONENT
    : E [+-]? DIGIT+
    ;

fragment DIGIT
    : [0-9]
    ;


fragment LETTER
    : [a-zA-Z$_] // these are the "java letters" below 0x7F
    | ~[\u0000-\u007F\uD800-\uDBFF] // covers all characters above 0x7F which are not a surrogate
    | [\uD800-\uDBFF] [\uDC00-\uDFFF] // covers UTF-16 surrogate pairs encodings for U+10000 to U+10FFFF
    ;

SIMPLE_COMMENT
    : '--' ('\\\n' | ~[\r\n])* '\r'? '\n'? -> channel(HIDDEN)
    ;

BRACKETED_COMMENT
    : COMMENT_START ( BRACKETED_COMMENT | . )*? ('*/' | {markUnclosedComment();} EOF) -> channel(2)
    ;


FROM_DUAL
    : FROM WS+ DUAL -> channel(HIDDEN);

WS
    : [ \r\n\t]+ -> channel(HIDDEN)
    ;

// Catch-all for anything we can't recognize.
// We use this to be able to ignore and recover all the text
// when splitting statements with DelimiterLexer
UNRECOGNIZED
    : .
    ;
