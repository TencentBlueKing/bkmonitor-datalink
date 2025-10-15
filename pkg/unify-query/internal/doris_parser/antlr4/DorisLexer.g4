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
    	return true
    }

    func markUnclosedComment() {
        has_unclosed_bracketed_comment = true;
    }
}

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
ACCOUNT_LOCK: 'ACCOUNT_LOCK' | 'account_lock';
ACCOUNT_UNLOCK: 'ACCOUNT_UNLOCK' | 'account_unlock';
ACTIONS: 'ACTIONS' | 'actions';
ADD: 'ADD' | 'add';
ADMIN: 'ADMIN' | 'admin';
AFTER: 'AFTER' | 'after';
AGG_STATE: 'AGG_STATE' | 'agg_state';
AGGREGATE: 'AGGREGATE' | 'aggregate';
ALIAS: 'ALIAS' | 'alias';
ALL: 'ALL' | 'all';
ALTER: 'ALTER' | 'alter';
ANALYZE: 'ANALYZE' | 'analyze';
ANALYZED: 'ANALYZED' | 'analyzed';
ANALYZER: 'ANALYZER' | 'analyzer';
AND: 'AND' | 'and';
ANTI: 'ANTI' | 'anti';
APPEND: 'APPEND' | 'append';
ARRAY: 'ARRAY' | 'array';
AS: 'AS' | 'as';
ASC: 'ASC' | 'asc';
AT: 'AT' | 'at';
AUTHORS: 'AUTHORS' | 'authors';
AUTO: 'AUTO' | 'auto';
AUTO_INCREMENT: 'AUTO_INCREMENT' | 'auto_increment';
ALWAYS: 'ALWAYS' | 'always';
BACKEND: 'BACKEND' | 'backend';
BACKENDS: 'BACKENDS' | 'backends';
BACKUP: 'BACKUP' | 'backup';
BEGIN: 'BEGIN' | 'begin';
BELONG: 'BELONG' | 'belong';
BETWEEN: 'BETWEEN' | 'between';
BIGINT: 'BIGINT' | 'bigint';
BIN: 'BIN' | 'bin';
BINARY: 'BINARY' | 'binary';
BINLOG: 'BINLOG' | 'binlog';
BITAND: 'BITAND' | 'bitand';
BITMAP: 'BITMAP' | 'bitmap';
BITMAP_EMPTY: 'BITMAP_EMPTY' | 'bitmap_empty';
BITMAP_UNION: 'BITMAP_UNION' | 'bitmap_union';
BITOR: 'BITOR' | 'bitor';
BITXOR: 'BITXOR' | 'bitxor';
BLOB: 'BLOB' | 'blob';
BOOLEAN: 'BOOLEAN' | 'boolean';
BRANCH: 'BRANCH' | 'branch';
BRIEF: 'BRIEF' | 'brief';
BROKER: 'BROKER' | 'broker';
BUCKETS: 'BUCKETS' | 'buckets';
BUILD: 'BUILD' | 'build';
BUILTIN: 'BUILTIN' | 'builtin';
BULK: 'BULK' | 'bulk';
BY: 'BY' | 'by';
CACHE: 'CACHE' | 'cache';
CACHED: 'CACHED' | 'cached';
CALL: 'CALL' | 'call';
CANCEL: 'CANCEL' | 'cancel';
CASE: 'CASE' | 'case';
CAST: 'CAST' | 'cast';
CATALOG: 'CATALOG' | 'catalog';
CATALOGS: 'CATALOGS' | 'catalogs';
CHAIN: 'CHAIN' | 'chain';
CHAR: 'CHAR' | 'char' | 'CHARACTER' | 'character';
CHARSET: 'CHARSET' | 'charset';
CHECK: 'CHECK' | 'check';
CLEAN: 'CLEAN' | 'clean';
CLUSTER: 'CLUSTER' | 'cluster';
CLUSTERS: 'CLUSTERS' | 'clusters';
COLLATE: 'COLLATE' | 'collate';
COLLATION: 'COLLATION' | 'collation';
COLLECT: 'COLLECT' | 'collect';
COLOCATE: 'COLOCATE' | 'colocate';
COLUMN: 'COLUMN' | 'column';
COLUMNS: 'COLUMNS' | 'columns';
COMMENT: 'COMMENT' | 'comment';
COMMIT: 'COMMIT' | 'commit';
COMMITTED: 'COMMITTED' | 'committed';
COMPACT: 'COMPACT' | 'compact';
COMPLETE: 'COMPLETE' | 'complete';
COMPRESS_TYPE: 'COMPRESS_TYPE' | 'compress_type';
COMPUTE: 'COMPUTE' | 'compute';
CONDITIONS: 'CONDITIONS' | 'conditions';
CONFIG: 'CONFIG' | 'config';
CONNECTION: 'CONNECTION' | 'connection';
CONNECTION_ID: 'CONNECTION_ID' | 'connection_id';
CONSISTENT: 'CONSISTENT' | 'consistent';
CONSTRAINT: 'CONSTRAINT' | 'constraint';
CONSTRAINTS: 'CONSTRAINTS' | 'constraints';
CONVERT: 'CONVERT' | 'convert';
CONVERT_LSC: 'CONVERT_LIGHT_SCHEMA_CHANGE_PROCESS' | 'convert_light_schema_change_process';
COPY: 'COPY' | 'copy';
COUNT: 'COUNT' | 'count';
CREATE: 'CREATE' | 'create';
CREATION: 'CREATION' | 'creation';
CRON: 'CRON' | 'cron';
CROSS: 'CROSS' | 'cross';
CUBE: 'CUBE' | 'cube';
CURRENT: 'CURRENT' | 'current';
CURRENT_CATALOG: 'CURRENT_CATALOG' | 'current_catalog';
CURRENT_DATE: 'CURRENT_DATE' | 'current_date';
CURRENT_TIME: 'CURRENT_TIME' | 'current_time';
CURRENT_TIMESTAMP: 'CURRENT_TIMESTAMP' | 'current_timestamp';
CURRENT_USER: 'CURRENT_USER' | 'current_user';
DATA: 'DATA' | 'data';
DATABASE: 'DATABASE' | 'database';
DATABASES: 'DATABASES' | 'databases';
DATE: 'DATE' | 'date';
DATETIME: 'DATETIME' | 'datetime';
DATETIMEV2: 'DATETIMEV2' | 'datetimev2';
DATEV2: 'DATEV2' | 'datev2';
DATETIMEV1: 'DATETIMEV1' | 'datetimev1';
DATEV1: 'DATEV1' | 'datev1';
DAY: 'DAY' | 'day';
DAYS: 'DAYS' | 'days';
DECIMAL: 'DECIMAL' | 'decimal';
DECIMALV2: 'DECIMALV2' | 'decimalv2';
DECIMALV3: 'DECIMALV3' | 'decimalv3';
DECOMMISSION: 'DECOMMISSION' | 'decommission';
DEFAULT: 'DEFAULT' | 'default';
DEFERRED: 'DEFERRED' | 'deferred';
DELETE: 'DELETE' | 'delete';
DEMAND: 'DEMAND' | 'demand';
DESC: 'DESC' | 'desc';
DESCRIBE: 'DESCRIBE' | 'describe';
DIAGNOSE: 'DIAGNOSE' | 'diagnose';
DIAGNOSIS: 'DIAGNOSIS' | 'diagnosis';
DICTIONARIES: 'DICTIONARIES' | 'dictionaries';
DICTIONARY: 'DICTIONARY' | 'dictionary';
DISK: 'DISK' | 'disk';
DISTINCT: 'DISTINCT' | 'distinct';
DISTINCTPC: 'DISTINCTPC' | 'distinctpc';
DISTINCTPCSA: 'DISTINCTPCSA' | 'distinctpcsa';
DISTRIBUTED: 'DISTRIBUTED' | 'distributed';
DISTRIBUTION: 'DISTRIBUTION' | 'distribution';
DIV: 'DIV' | 'div';
DO: 'DO' | 'do';
DORIS_INTERNAL_TABLE_ID: 'DORIS_INTERNAL_TABLE_ID' | 'doris_internal_table_id';
DOUBLE: 'DOUBLE' | 'double';
DROP: 'DROP' | 'drop';
DROPP: 'DROPP' | 'dropp';
DUAL: 'DUAL' | 'dual';
DUMP: 'DUMP' | 'dump';
DUPLICATE: 'DUPLICATE' | 'duplicate';
DYNAMIC: 'DYNAMIC' | 'dynamic';
E: 'E' | 'e';
ELSE: 'ELSE' | 'else';
ENABLE: 'ENABLE' | 'enable';
ENCRYPTKEY: 'ENCRYPTKEY' | 'encryptkey';
ENCRYPTKEYS: 'ENCRYPTKEYS' | 'encryptkeys';
END: 'END' | 'end';
ENDS: 'ENDS' | 'ends';
ENGINE: 'ENGINE' | 'engine';
ENGINES: 'ENGINES' | 'engines';
ENTER: 'ENTER' | 'enter';
ERRORS: 'ERRORS' | 'errors';
ESCAPE: 'ESCAPE' | 'escape';
EVENTS: 'EVENTS' | 'events';
EVERY: 'EVERY' | 'every';
EXCEPT: 'EXCEPT' | 'except';
EXCLUDE: 'EXCLUDE' | 'exclude';
EXECUTE: 'EXECUTE' | 'execute';
EXISTS: 'EXISTS' | 'exists';
EXPIRED: 'EXPIRED' | 'expired';
EXPLAIN: 'EXPLAIN' | 'explain';
EXPORT: 'EXPORT' | 'export';
EXTENDED: 'EXTENDED' | 'extended';
EXTERNAL: 'EXTERNAL' | 'external';
EXTRACT: 'EXTRACT' | 'extract';
FAILED_LOGIN_ATTEMPTS: 'FAILED_LOGIN_ATTEMPTS' | 'failed_login_attempts';
FALSE: 'FALSE' | 'false';
FAST: 'FAST' | 'fast';
FEATURE: 'FEATURE' | 'feature';
FIELDS: 'FIELDS' | 'fields';
FILE: 'FILE' | 'file';
FILTER: 'FILTER' | 'filter';
FIRST: 'FIRST' | 'first';
FLOAT: 'FLOAT' | 'float';
FOLLOWER: 'FOLLOWER' | 'follower';
FOLLOWING: 'FOLLOWING' | 'following';
FOR: 'FOR' | 'for';
FOREIGN: 'FOREIGN' | 'foreign';
FORCE: 'FORCE' | 'force';
FORMAT: 'FORMAT' | 'format';
FREE: 'FREE' | 'free';
FROM: 'FROM' | 'from';
FRONTEND: 'FRONTEND' | 'frontend';
FRONTENDS: 'FRONTENDS' | 'frontends';
FULL: 'FULL' | 'full';
FUNCTION: 'FUNCTION' | 'function';
FUNCTIONS: 'FUNCTIONS' | 'functions';
GENERATED: 'GENERATED' | 'generated';
GENERIC: 'GENERIC' | 'generic';
GLOBAL: 'GLOBAL' | 'global';
GRANT: 'GRANT' | 'grant';
GRANTS: 'GRANTS' | 'grants';
GRAPH: 'GRAPH' | 'graph';
GROUP: 'GROUP' | 'group';
GROUPING: 'GROUPING' | 'grouping';
GROUPS: 'GROUPS' | 'groups';
HASH: 'HASH' | 'hash';
HASH_MAP: 'HASH_MAP' | 'hash_map';
HAVING: 'HAVING' | 'having';
HDFS: 'HDFS' | 'hdfs';
HELP: 'HELP' | 'help';
HISTOGRAM: 'HISTOGRAM' | 'histogram';
HLL: 'HLL' | 'hll';
HLL_UNION: 'HLL_UNION' | 'hll_union';
HOSTNAME: 'HOSTNAME' | 'hostname';
HOTSPOT: 'HOTSPOT' | 'hotspot';
HOUR: 'HOUR' | 'hour';
HOURS: 'HOURS' | 'hours';
HUB: 'HUB' | 'hub';
IDENTIFIED: 'IDENTIFIED' | 'identified';
IF: 'IF' | 'if';
IGNORE: 'IGNORE' | 'ignore';
IMMEDIATE: 'IMMEDIATE' | 'immediate';
IN: 'IN' | 'in';
INCREMENTAL: 'INCREMENTAL' | 'incremental';
INDEX: 'INDEX' | 'index';
INDEXES: 'INDEXES' | 'indexes';
INFILE: 'INFILE' | 'infile';
INNER: 'INNER' | 'inner';
INSERT: 'INSERT' | 'insert';
INSTALL: 'INSTALL' | 'install';
INT: 'INT' | 'int';
INTEGER: 'INTEGER' | 'integer';
INTERMEDIATE: 'INTERMEDIATE' | 'intermediate';
INTERSECT: 'INTERSECT' | 'intersect';
INTERVAL: 'INTERVAL' | 'interval';
INTO: 'INTO' | 'into';
INVERTED: 'INVERTED' | 'inverted';
IP_TRIE: 'IP_TRIE' | 'ip_trie';
IPV4: 'IPV4' | 'ipv4';
IPV6: 'IPV6' | 'ipv6';
IS: 'IS' | 'is';
IS_NOT_NULL_PRED: 'IS_NOT_NULL_PRED' | 'is_not_null_pred';
IS_NULL_PRED: 'IS_NULL_PRED' | 'is_null_pred';
ISNULL: 'ISNULL' | 'isnull';
ISOLATION: 'ISOLATION' | 'isolation';
JOB: 'JOB' | 'job';
JOBS: 'JOBS' | 'jobs';
JOIN: 'JOIN' | 'join';
JSON: 'JSON' | 'json';
JSONB: 'JSONB' | 'jsonb';
KEY: 'KEY' | 'key';
KEYS: 'KEYS' | 'keys';
KILL: 'KILL' | 'kill';
LABEL: 'LABEL' | 'label';
LARGEINT: 'LARGEINT' | 'largeint';
LAYOUT: 'LAYOUT' | 'layout';
LAST: 'LAST' | 'last';
LATERAL: 'LATERAL' | 'lateral';
LDAP: 'LDAP' | 'ldap';
LDAP_ADMIN_PASSWORD: 'LDAP_ADMIN_PASSWORD' | 'ldap_admin_password';
LEFT: 'LEFT' | 'left';
LESS: 'LESS' | 'less';
LEVEL: 'LEVEL' | 'level';
LIKE: 'LIKE' | 'like';
LIMIT: 'LIMIT' | 'limit';
LINES: 'LINES' | 'lines';
LINK: 'LINK' | 'link';
LIST: 'LIST' | 'list';
LOAD: 'LOAD' | 'load';
LOCAL: 'LOCAL' | 'local';
LOCALTIME: 'LOCALTIME' | 'localtime';
LOCALTIMESTAMP: 'LOCALTIMESTAMP' | 'localtimestamp';
LOCATION: 'LOCATION' | 'location';
LOCK: 'LOCK' | 'lock';
LOGICAL: 'LOGICAL' | 'logical';
LOW_PRIORITY: 'LOW_PRIORITY' | 'low_priority';
MANUAL: 'MANUAL' | 'manual';
MAP: 'MAP' | 'map';
MATCH: 'MATCH' | 'match';
MATCH_ALL: 'MATCH_ALL' | 'match_all';
MATCH_ANY: 'MATCH_ANY' | 'match_any';
MATCH_PHRASE: 'MATCH_PHRASE' | 'match_phrase';
MATCH_PHRASE_EDGE: 'MATCH_PHRASE_EDGE' | 'match_phrase_edge';
MATCH_PHRASE_PREFIX: 'MATCH_PHRASE_PREFIX' | 'match_phrase_prefix';
MATCH_REGEXP: 'MATCH_REGEXP' | 'match_regexp';
MATERIALIZED: 'MATERIALIZED' | 'materialized';
MAX: 'MAX' | 'max';
MAXVALUE: 'MAXVALUE' | 'maxvalue';
MEMO: 'MEMO' | 'memo';
MERGE: 'MERGE' | 'merge';
MIGRATE: 'MIGRATE' | 'migrate';
MIGRATIONS: 'MIGRATIONS' | 'migrations';
MIN: 'MIN' | 'min';
MINUS: 'MINUS' | 'minus';
MINUTE: 'MINUTE' | 'minute';
MINUTES: 'MINUTES' | 'minutes';
MODIFY: 'MODIFY' | 'modify';
MONTH: 'MONTH' | 'month';
MTMV: 'MTMV' | 'mtmv';
NAME: 'NAME' | 'name';
NAMES: 'NAMES' | 'names';
NATURAL: 'NATURAL' | 'natural';
NEGATIVE: 'NEGATIVE' | 'negative';
NEVER: 'NEVER' | 'never';
NEXT: 'NEXT' | 'next';
NGRAM_BF: 'NGRAM_BF' | 'ngram_bf';
NO: 'NO' | 'no';
NO_USE_MV: 'NO_USE_MV' | 'no_use_mv';
NON_NULLABLE: 'NON_NULLABLE' | 'non_nullable';
NOT: 'NOT' | 'not';
NULL: 'NULL' | 'null';
NULLS: 'NULLS' | 'nulls';
OBSERVER: 'OBSERVER' | 'observer';
OF: 'OF' | 'of';
OFFSET: 'OFFSET' | 'offset';
ON: 'ON' | 'on';
ONLY: 'ONLY' | 'only';
OPEN: 'OPEN' | 'open';
OPTIMIZED: 'OPTIMIZED' | 'optimized';
OR: 'OR' | 'or';
ORDER: 'ORDER' | 'order';
OUTER: 'OUTER' | 'outer';
OUTFILE: 'OUTFILE' | 'outfile';
OVER: 'OVER' | 'over';
OVERWRITE: 'OVERWRITE' | 'overwrite';
PARAMETER: 'PARAMETER' | 'parameter';
PARSED: 'PARSED' | 'parsed';
PARTITION: 'PARTITION' | 'partition';
PARTITIONS: 'PARTITIONS' | 'partitions';
PASSWORD: 'PASSWORD' | 'password';
PASSWORD_EXPIRE: 'PASSWORD_EXPIRE' | 'password_expire';
PASSWORD_HISTORY: 'PASSWORD_HISTORY' | 'password_history';
PASSWORD_LOCK_TIME: 'PASSWORD_LOCK_TIME' | 'password_lock_time';
PASSWORD_REUSE: 'PASSWORD_REUSE' | 'password_reuse';
PATH: 'PATH' | 'path';
PAUSE: 'PAUSE' | 'pause';
PERCENT: 'PERCENT' | 'percent';
PERIOD: 'PERIOD' | 'period';
PERMISSIVE: 'PERMISSIVE' | 'permissive';
PHYSICAL: 'PHYSICAL' | 'physical';
PI: 'PI' | 'pi';
PLACEHOLDER: '?';
PLAN: 'PLAN' | 'plan';
PLAY: 'PLAY' | 'play';
PRIVILEGES: 'PRIVILEGES' | 'privileges';
PROCESS: 'PROCESS' | 'process';
PLUGIN: 'PLUGIN' | 'plugin';
PLUGINS: 'PLUGINS' | 'plugins';
POLICY: 'POLICY' | 'policy';
PRECEDING: 'PRECEDING' | 'preceding';
PREPARE: 'PREPARE' | 'prepare';
PRIMARY: 'PRIMARY' | 'primary';
PROC: 'PROC' | 'proc';
PROCEDURE: 'PROCEDURE' | 'procedure';
PROCESSLIST: 'PROCESSLIST' | 'processlist';
PROFILE: 'PROFILE' | 'profile';
PROPERTIES: 'PROPERTIES' | 'properties';
PROPERTY: 'PROPERTY' | 'property';
QUANTILE_STATE: 'QUANTILE_STATE' | 'quantile_state';
QUANTILE_UNION: 'QUANTILE_UNION' | 'quantile_union';
QUERY: 'QUERY' | 'query';
QUEUED: 'QUEUED' | 'queued';
QUOTA: 'QUOTA' | 'quota';
QUALIFY: 'QUALIFY' | 'qualify';
QUARTER: 'QUARTER' | 'quarter';
RANDOM: 'RANDOM' | 'random';
RANGE: 'RANGE' | 'range';
READ: 'READ' | 'read';
REAL: 'REAL' | 'real';
REBALANCE: 'REBALANCE' | 'rebalance';
RECENT: 'RECENT' | 'recent';
RECOVER: 'RECOVER' | 'recover';
RECYCLE: 'RECYCLE' | 'recycle';
REFRESH: 'REFRESH' | 'refresh';
REFERENCES: 'REFERENCES' | 'references';
REGEXP: 'REGEXP' | 'regexp';
RELEASE: 'RELEASE' | 'release';
RENAME: 'RENAME' | 'rename';
REPAIR: 'REPAIR' | 'repair';
REPEATABLE: 'REPEATABLE' | 'repeatable';
REPLACE: 'REPLACE' | 'replace';
REPLACE_IF_NOT_NULL: 'REPLACE_IF_NOT_NULL' | 'replace_if_not_null';
REPLAYER: 'REPLAYER' | 'replayer';
REPLICA: 'REPLICA' | 'replica';
REPOSITORIES: 'REPOSITORIES' | 'repositories';
REPOSITORY: 'REPOSITORY' | 'repository';
RESOURCE: 'RESOURCE' | 'resource';
RESOURCES: 'RESOURCES' | 'resources';
RESTORE: 'RESTORE' | 'restore';
RESTRICTIVE: 'RESTRICTIVE' | 'restrictive';
RESUME: 'RESUME' | 'resume';
RETAIN: 'RETAIN' | 'retain';
RETENTION: 'RETENTION' | 'retention';
RETURNS: 'RETURNS' | 'returns';
REVOKE: 'REVOKE' | 'revoke';
REWRITTEN: 'REWRITTEN' | 'rewritten';
RIGHT: 'RIGHT' | 'right';
RLIKE: 'RLIKE' | 'rlike';
ROLE: 'ROLE' | 'role';
ROLES: 'ROLES' | 'roles';
ROLLBACK: 'ROLLBACK' | 'rollback';
ROLLUP: 'ROLLUP' | 'rollup';
ROUTINE: 'ROUTINE' | 'routine';
ROW: 'ROW' | 'row';
ROWS: 'ROWS' | 'rows';
S3: 'S3' | 's3';
SAMPLE: 'SAMPLE' | 'sample';
SCHEDULE: 'SCHEDULE' | 'schedule';
SCHEDULER: 'SCHEDULER' | 'scheduler';
SCHEMA: 'SCHEMA' | 'schema';
SCHEMAS: 'SCHEMAS' | 'schemas';
SECOND: 'SECOND' | 'second';
SELECT: 'SELECT' | 'select';
SEMI: 'SEMI' | 'semi';
SERIALIZABLE: 'SERIALIZABLE' | 'serializable';
SESSION: 'SESSION' | 'session';
SESSION_USER: 'SESSION_USER' | 'session_user';
SET: 'SET' | 'set';
SETS: 'SETS' | 'sets';
SET_SESSION_VARIABLE: 'SET_SESSION_VARIABLE' | 'set_session_variable';
SHAPE: 'SHAPE' | 'shape';
SHOW: 'SHOW' | 'show';
SIGNED: 'SIGNED' | 'signed';
SKEW: 'SKEW' | 'skew';
SMALLINT: 'SMALLINT' | 'smallint';
SNAPSHOT: 'SNAPSHOT' | 'snapshot';
SNAPSHOTS: 'SNAPSHOTS' | 'snapshots';
SONAME: 'SONAME' | 'soname';
SPLIT: 'SPLIT' | 'split';
SQL: 'SQL' | 'sql';
SQL_BLOCK_RULE: 'SQL_BLOCK_RULE' | 'sql_block_rule';
STAGE: 'STAGE' | 'stage';
STAGES: 'STAGES' | 'stages';
START: 'START' | 'start';
STARTS: 'STARTS' | 'starts';
STATS: 'STATS' | 'stats';
STATUS: 'STATUS' | 'status';
STOP: 'STOP' | 'stop';
STORAGE: 'STORAGE' | 'storage';
STREAM: 'STREAM' | 'stream';
STREAMING: 'STREAMING' | 'streaming';
STRING: 'STRING' | 'string';
STRUCT: 'STRUCT' | 'struct';
SUM: 'SUM' | 'sum';
SUPERUSER: 'SUPERUSER' | 'superuser';
SWITCH: 'SWITCH' | 'switch';
SYNC: 'SYNC' | 'sync';
SYSTEM: 'SYSTEM' | 'system';
TABLE: 'TABLE' | 'table';
TABLES: 'TABLES' | 'tables';
TABLESAMPLE: 'TABLESAMPLE' | 'tablesample';
TABLET: 'TABLET' | 'tablet';
TABLETS: 'TABLETS' | 'tablets';
TAG: 'TAG' | 'tag';
TASK: 'TASK' | 'task';
TASKS: 'TASKS' | 'tasks';
TEMPORARY: 'TEMPORARY' | 'temporary';
TERMINATED: 'TERMINATED' | 'terminated';
TEXT: 'TEXT' | 'text';
THAN: 'THAN' | 'than';
THEN: 'THEN' | 'then';
TIME: 'TIME' | 'time';
TIMESTAMP: 'TIMESTAMP' | 'timestamp';
TINYINT: 'TINYINT' | 'tinyint';
TO: 'TO' | 'to';
TOKENIZER: 'TOKENIZER' | 'tokenizer';
TOKEN_FILTER: 'TOKEN_FILTER' | 'token_filter';
TRANSACTION: 'TRANSACTION' | 'transaction';
TRASH: 'TRASH' | 'trash';
TREE: 'TREE' | 'tree';
TRIGGERS: 'TRIGGERS' | 'triggers';
TRIM: 'TRIM' | 'trim';
TRUE: 'TRUE' | 'true';
TRUNCATE: 'TRUNCATE' | 'truncate';
TYPE: 'TYPE' | 'type';
TYPECAST: 'TYPE_CAST' | 'type_cast';
TYPES: 'TYPES' | 'types';
UNBOUNDED: 'UNBOUNDED' | 'unbounded';
UNCOMMITTED: 'UNCOMMITTED' | 'uncommitted';
UNINSTALL: 'UNINSTALL' | 'uninstall';
UNION: 'UNION' | 'union';
UNIQUE: 'UNIQUE' | 'unique';
UNLOCK: 'UNLOCK' | 'unlock';
UNSET: 'UNSET' | 'unset';
UNSIGNED: 'UNSIGNED' | 'unsigned';
UP: 'UP' | 'up';
UPDATE: 'UPDATE' | 'update';
USE: 'USE' | 'use';
USER: 'USER' | 'user';
USE_MV: 'USE_MV' | 'use_mv';
USING: 'USING' | 'using';
VALUE: 'VALUE' | 'value';
VALUES: 'VALUES' | 'values';
VARCHAR: 'VARCHAR' | 'varchar';
VARIABLE: 'VARIABLE' | 'variable';
VARIABLES: 'VARIABLES' | 'variables';
VARIANT: 'VARIANT' | 'variant';
VAULT: 'VAULT' | 'vault';
VAULTS: 'VAULTS' | 'vaults';
VERBOSE: 'VERBOSE' | 'verbose';
VERSION: 'VERSION' | 'version';
VIEW: 'VIEW' | 'view';
VIEWS: 'VIEWS' | 'views';
WARM: 'WARM' | 'warm';
WARNINGS: 'WARNINGS' | 'warnings';
WEEK: 'WEEK' | 'week';
WHEN: 'WHEN' | 'when';
WHERE: 'WHERE' | 'where';
WHITELIST: 'WHITELIST' | 'whitelist';
WITH: 'WITH' | 'with';
WORK: 'WORK' | 'work';
WORKLOAD: 'WORKLOAD';
WRITE: 'WRITE' | 'write';
XOR: 'XOR' | 'xor';
YEAR: 'YEAR' | 'year';
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
    : DIGIT+ 'L'
    ;

SMALLINT_LITERAL
    : DIGIT+ 'S'
    ;

TINYINT_LITERAL
    : DIGIT+ 'Y'
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
    : DIGIT+ EXPONENT? 'BD'
    | DECIMAL_DIGITS EXPONENT? 'BD' {isValidDecimal(localctx)}?
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
    : 'E' [+-]? DIGIT+
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
    : 'FROM' WS+ 'DUAL' -> channel(HIDDEN);

WS
    : [ \r\n\t]+ -> channel(HIDDEN)
    ;

// Catch-all for anything we can't recognize.
// We use this to be able to ignore and recover all the text
// when splitting statements with DelimiterLexer
UNRECOGNIZED
    : .
    ;
