// Code generated from DorisParser.g4 by ANTLR 4.13.2. DO NOT EDIT.

package gen // DorisParser
import "github.com/antlr4-go/antlr/v4"

// BaseDorisParserListener is a complete listener for a parse tree produced by DorisParserParser.
type BaseDorisParserListener struct{}

var _ DorisParserListener = &BaseDorisParserListener{}

// VisitTerminal is called when a terminal node is visited.
func (s *BaseDorisParserListener) VisitTerminal(node antlr.TerminalNode) {}

// VisitErrorNode is called when an error node is visited.
func (s *BaseDorisParserListener) VisitErrorNode(node antlr.ErrorNode) {}

// EnterEveryRule is called when any rule is entered.
func (s *BaseDorisParserListener) EnterEveryRule(ctx antlr.ParserRuleContext) {}

// ExitEveryRule is called when any rule is exited.
func (s *BaseDorisParserListener) ExitEveryRule(ctx antlr.ParserRuleContext) {}

// EnterMultiStatements is called when production multiStatements is entered.
func (s *BaseDorisParserListener) EnterMultiStatements(ctx *MultiStatementsContext) {}

// ExitMultiStatements is called when production multiStatements is exited.
func (s *BaseDorisParserListener) ExitMultiStatements(ctx *MultiStatementsContext) {}

// EnterSingleStatement is called when production singleStatement is entered.
func (s *BaseDorisParserListener) EnterSingleStatement(ctx *SingleStatementContext) {}

// ExitSingleStatement is called when production singleStatement is exited.
func (s *BaseDorisParserListener) ExitSingleStatement(ctx *SingleStatementContext) {}

// EnterStatementBaseAlias is called when production statementBaseAlias is entered.
func (s *BaseDorisParserListener) EnterStatementBaseAlias(ctx *StatementBaseAliasContext) {}

// ExitStatementBaseAlias is called when production statementBaseAlias is exited.
func (s *BaseDorisParserListener) ExitStatementBaseAlias(ctx *StatementBaseAliasContext) {}

// EnterCallProcedure is called when production callProcedure is entered.
func (s *BaseDorisParserListener) EnterCallProcedure(ctx *CallProcedureContext) {}

// ExitCallProcedure is called when production callProcedure is exited.
func (s *BaseDorisParserListener) ExitCallProcedure(ctx *CallProcedureContext) {}

// EnterCreateProcedure is called when production createProcedure is entered.
func (s *BaseDorisParserListener) EnterCreateProcedure(ctx *CreateProcedureContext) {}

// ExitCreateProcedure is called when production createProcedure is exited.
func (s *BaseDorisParserListener) ExitCreateProcedure(ctx *CreateProcedureContext) {}

// EnterDropProcedure is called when production dropProcedure is entered.
func (s *BaseDorisParserListener) EnterDropProcedure(ctx *DropProcedureContext) {}

// ExitDropProcedure is called when production dropProcedure is exited.
func (s *BaseDorisParserListener) ExitDropProcedure(ctx *DropProcedureContext) {}

// EnterShowProcedureStatus is called when production showProcedureStatus is entered.
func (s *BaseDorisParserListener) EnterShowProcedureStatus(ctx *ShowProcedureStatusContext) {}

// ExitShowProcedureStatus is called when production showProcedureStatus is exited.
func (s *BaseDorisParserListener) ExitShowProcedureStatus(ctx *ShowProcedureStatusContext) {}

// EnterShowCreateProcedure is called when production showCreateProcedure is entered.
func (s *BaseDorisParserListener) EnterShowCreateProcedure(ctx *ShowCreateProcedureContext) {}

// ExitShowCreateProcedure is called when production showCreateProcedure is exited.
func (s *BaseDorisParserListener) ExitShowCreateProcedure(ctx *ShowCreateProcedureContext) {}

// EnterShowConfig is called when production showConfig is entered.
func (s *BaseDorisParserListener) EnterShowConfig(ctx *ShowConfigContext) {}

// ExitShowConfig is called when production showConfig is exited.
func (s *BaseDorisParserListener) ExitShowConfig(ctx *ShowConfigContext) {}

// EnterStatementDefault is called when production statementDefault is entered.
func (s *BaseDorisParserListener) EnterStatementDefault(ctx *StatementDefaultContext) {}

// ExitStatementDefault is called when production statementDefault is exited.
func (s *BaseDorisParserListener) ExitStatementDefault(ctx *StatementDefaultContext) {}

// EnterSupportedDmlStatementAlias is called when production supportedDmlStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedDmlStatementAlias(ctx *SupportedDmlStatementAliasContext) {
}

// ExitSupportedDmlStatementAlias is called when production supportedDmlStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedDmlStatementAlias(ctx *SupportedDmlStatementAliasContext) {
}

// EnterSupportedCreateStatementAlias is called when production supportedCreateStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedCreateStatementAlias(ctx *SupportedCreateStatementAliasContext) {
}

// ExitSupportedCreateStatementAlias is called when production supportedCreateStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedCreateStatementAlias(ctx *SupportedCreateStatementAliasContext) {
}

// EnterSupportedAlterStatementAlias is called when production supportedAlterStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedAlterStatementAlias(ctx *SupportedAlterStatementAliasContext) {
}

// ExitSupportedAlterStatementAlias is called when production supportedAlterStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedAlterStatementAlias(ctx *SupportedAlterStatementAliasContext) {
}

// EnterMaterializedViewStatementAlias is called when production materializedViewStatementAlias is entered.
func (s *BaseDorisParserListener) EnterMaterializedViewStatementAlias(ctx *MaterializedViewStatementAliasContext) {
}

// ExitMaterializedViewStatementAlias is called when production materializedViewStatementAlias is exited.
func (s *BaseDorisParserListener) ExitMaterializedViewStatementAlias(ctx *MaterializedViewStatementAliasContext) {
}

// EnterSupportedJobStatementAlias is called when production supportedJobStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedJobStatementAlias(ctx *SupportedJobStatementAliasContext) {
}

// ExitSupportedJobStatementAlias is called when production supportedJobStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedJobStatementAlias(ctx *SupportedJobStatementAliasContext) {
}

// EnterConstraintStatementAlias is called when production constraintStatementAlias is entered.
func (s *BaseDorisParserListener) EnterConstraintStatementAlias(ctx *ConstraintStatementAliasContext) {
}

// ExitConstraintStatementAlias is called when production constraintStatementAlias is exited.
func (s *BaseDorisParserListener) ExitConstraintStatementAlias(ctx *ConstraintStatementAliasContext) {
}

// EnterSupportedCleanStatementAlias is called when production supportedCleanStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedCleanStatementAlias(ctx *SupportedCleanStatementAliasContext) {
}

// ExitSupportedCleanStatementAlias is called when production supportedCleanStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedCleanStatementAlias(ctx *SupportedCleanStatementAliasContext) {
}

// EnterSupportedDescribeStatementAlias is called when production supportedDescribeStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedDescribeStatementAlias(ctx *SupportedDescribeStatementAliasContext) {
}

// ExitSupportedDescribeStatementAlias is called when production supportedDescribeStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedDescribeStatementAlias(ctx *SupportedDescribeStatementAliasContext) {
}

// EnterSupportedDropStatementAlias is called when production supportedDropStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedDropStatementAlias(ctx *SupportedDropStatementAliasContext) {
}

// ExitSupportedDropStatementAlias is called when production supportedDropStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedDropStatementAlias(ctx *SupportedDropStatementAliasContext) {
}

// EnterSupportedSetStatementAlias is called when production supportedSetStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedSetStatementAlias(ctx *SupportedSetStatementAliasContext) {
}

// ExitSupportedSetStatementAlias is called when production supportedSetStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedSetStatementAlias(ctx *SupportedSetStatementAliasContext) {
}

// EnterSupportedUnsetStatementAlias is called when production supportedUnsetStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedUnsetStatementAlias(ctx *SupportedUnsetStatementAliasContext) {
}

// ExitSupportedUnsetStatementAlias is called when production supportedUnsetStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedUnsetStatementAlias(ctx *SupportedUnsetStatementAliasContext) {
}

// EnterSupportedRefreshStatementAlias is called when production supportedRefreshStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedRefreshStatementAlias(ctx *SupportedRefreshStatementAliasContext) {
}

// ExitSupportedRefreshStatementAlias is called when production supportedRefreshStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedRefreshStatementAlias(ctx *SupportedRefreshStatementAliasContext) {
}

// EnterSupportedShowStatementAlias is called when production supportedShowStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedShowStatementAlias(ctx *SupportedShowStatementAliasContext) {
}

// ExitSupportedShowStatementAlias is called when production supportedShowStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedShowStatementAlias(ctx *SupportedShowStatementAliasContext) {
}

// EnterSupportedLoadStatementAlias is called when production supportedLoadStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedLoadStatementAlias(ctx *SupportedLoadStatementAliasContext) {
}

// ExitSupportedLoadStatementAlias is called when production supportedLoadStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedLoadStatementAlias(ctx *SupportedLoadStatementAliasContext) {
}

// EnterSupportedCancelStatementAlias is called when production supportedCancelStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedCancelStatementAlias(ctx *SupportedCancelStatementAliasContext) {
}

// ExitSupportedCancelStatementAlias is called when production supportedCancelStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedCancelStatementAlias(ctx *SupportedCancelStatementAliasContext) {
}

// EnterSupportedRecoverStatementAlias is called when production supportedRecoverStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedRecoverStatementAlias(ctx *SupportedRecoverStatementAliasContext) {
}

// ExitSupportedRecoverStatementAlias is called when production supportedRecoverStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedRecoverStatementAlias(ctx *SupportedRecoverStatementAliasContext) {
}

// EnterSupportedAdminStatementAlias is called when production supportedAdminStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedAdminStatementAlias(ctx *SupportedAdminStatementAliasContext) {
}

// ExitSupportedAdminStatementAlias is called when production supportedAdminStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedAdminStatementAlias(ctx *SupportedAdminStatementAliasContext) {
}

// EnterSupportedUseStatementAlias is called when production supportedUseStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedUseStatementAlias(ctx *SupportedUseStatementAliasContext) {
}

// ExitSupportedUseStatementAlias is called when production supportedUseStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedUseStatementAlias(ctx *SupportedUseStatementAliasContext) {
}

// EnterSupportedOtherStatementAlias is called when production supportedOtherStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedOtherStatementAlias(ctx *SupportedOtherStatementAliasContext) {
}

// ExitSupportedOtherStatementAlias is called when production supportedOtherStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedOtherStatementAlias(ctx *SupportedOtherStatementAliasContext) {
}

// EnterSupportedKillStatementAlias is called when production supportedKillStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedKillStatementAlias(ctx *SupportedKillStatementAliasContext) {
}

// ExitSupportedKillStatementAlias is called when production supportedKillStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedKillStatementAlias(ctx *SupportedKillStatementAliasContext) {
}

// EnterSupportedStatsStatementAlias is called when production supportedStatsStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedStatsStatementAlias(ctx *SupportedStatsStatementAliasContext) {
}

// ExitSupportedStatsStatementAlias is called when production supportedStatsStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedStatsStatementAlias(ctx *SupportedStatsStatementAliasContext) {
}

// EnterSupportedTransactionStatementAlias is called when production supportedTransactionStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedTransactionStatementAlias(ctx *SupportedTransactionStatementAliasContext) {
}

// ExitSupportedTransactionStatementAlias is called when production supportedTransactionStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedTransactionStatementAlias(ctx *SupportedTransactionStatementAliasContext) {
}

// EnterSupportedGrantRevokeStatementAlias is called when production supportedGrantRevokeStatementAlias is entered.
func (s *BaseDorisParserListener) EnterSupportedGrantRevokeStatementAlias(ctx *SupportedGrantRevokeStatementAliasContext) {
}

// ExitSupportedGrantRevokeStatementAlias is called when production supportedGrantRevokeStatementAlias is exited.
func (s *BaseDorisParserListener) ExitSupportedGrantRevokeStatementAlias(ctx *SupportedGrantRevokeStatementAliasContext) {
}

// EnterUnsupported is called when production unsupported is entered.
func (s *BaseDorisParserListener) EnterUnsupported(ctx *UnsupportedContext) {}

// ExitUnsupported is called when production unsupported is exited.
func (s *BaseDorisParserListener) ExitUnsupported(ctx *UnsupportedContext) {}

// EnterUnsupportedStatement is called when production unsupportedStatement is entered.
func (s *BaseDorisParserListener) EnterUnsupportedStatement(ctx *UnsupportedStatementContext) {}

// ExitUnsupportedStatement is called when production unsupportedStatement is exited.
func (s *BaseDorisParserListener) ExitUnsupportedStatement(ctx *UnsupportedStatementContext) {}

// EnterCreateMTMV is called when production createMTMV is entered.
func (s *BaseDorisParserListener) EnterCreateMTMV(ctx *CreateMTMVContext) {}

// ExitCreateMTMV is called when production createMTMV is exited.
func (s *BaseDorisParserListener) ExitCreateMTMV(ctx *CreateMTMVContext) {}

// EnterRefreshMTMV is called when production refreshMTMV is entered.
func (s *BaseDorisParserListener) EnterRefreshMTMV(ctx *RefreshMTMVContext) {}

// ExitRefreshMTMV is called when production refreshMTMV is exited.
func (s *BaseDorisParserListener) ExitRefreshMTMV(ctx *RefreshMTMVContext) {}

// EnterAlterMTMV is called when production alterMTMV is entered.
func (s *BaseDorisParserListener) EnterAlterMTMV(ctx *AlterMTMVContext) {}

// ExitAlterMTMV is called when production alterMTMV is exited.
func (s *BaseDorisParserListener) ExitAlterMTMV(ctx *AlterMTMVContext) {}

// EnterDropMTMV is called when production dropMTMV is entered.
func (s *BaseDorisParserListener) EnterDropMTMV(ctx *DropMTMVContext) {}

// ExitDropMTMV is called when production dropMTMV is exited.
func (s *BaseDorisParserListener) ExitDropMTMV(ctx *DropMTMVContext) {}

// EnterPauseMTMV is called when production pauseMTMV is entered.
func (s *BaseDorisParserListener) EnterPauseMTMV(ctx *PauseMTMVContext) {}

// ExitPauseMTMV is called when production pauseMTMV is exited.
func (s *BaseDorisParserListener) ExitPauseMTMV(ctx *PauseMTMVContext) {}

// EnterResumeMTMV is called when production resumeMTMV is entered.
func (s *BaseDorisParserListener) EnterResumeMTMV(ctx *ResumeMTMVContext) {}

// ExitResumeMTMV is called when production resumeMTMV is exited.
func (s *BaseDorisParserListener) ExitResumeMTMV(ctx *ResumeMTMVContext) {}

// EnterCancelMTMVTask is called when production cancelMTMVTask is entered.
func (s *BaseDorisParserListener) EnterCancelMTMVTask(ctx *CancelMTMVTaskContext) {}

// ExitCancelMTMVTask is called when production cancelMTMVTask is exited.
func (s *BaseDorisParserListener) ExitCancelMTMVTask(ctx *CancelMTMVTaskContext) {}

// EnterShowCreateMTMV is called when production showCreateMTMV is entered.
func (s *BaseDorisParserListener) EnterShowCreateMTMV(ctx *ShowCreateMTMVContext) {}

// ExitShowCreateMTMV is called when production showCreateMTMV is exited.
func (s *BaseDorisParserListener) ExitShowCreateMTMV(ctx *ShowCreateMTMVContext) {}

// EnterCreateScheduledJob is called when production createScheduledJob is entered.
func (s *BaseDorisParserListener) EnterCreateScheduledJob(ctx *CreateScheduledJobContext) {}

// ExitCreateScheduledJob is called when production createScheduledJob is exited.
func (s *BaseDorisParserListener) ExitCreateScheduledJob(ctx *CreateScheduledJobContext) {}

// EnterPauseJob is called when production pauseJob is entered.
func (s *BaseDorisParserListener) EnterPauseJob(ctx *PauseJobContext) {}

// ExitPauseJob is called when production pauseJob is exited.
func (s *BaseDorisParserListener) ExitPauseJob(ctx *PauseJobContext) {}

// EnterDropJob is called when production dropJob is entered.
func (s *BaseDorisParserListener) EnterDropJob(ctx *DropJobContext) {}

// ExitDropJob is called when production dropJob is exited.
func (s *BaseDorisParserListener) ExitDropJob(ctx *DropJobContext) {}

// EnterResumeJob is called when production resumeJob is entered.
func (s *BaseDorisParserListener) EnterResumeJob(ctx *ResumeJobContext) {}

// ExitResumeJob is called when production resumeJob is exited.
func (s *BaseDorisParserListener) ExitResumeJob(ctx *ResumeJobContext) {}

// EnterCancelJobTask is called when production cancelJobTask is entered.
func (s *BaseDorisParserListener) EnterCancelJobTask(ctx *CancelJobTaskContext) {}

// ExitCancelJobTask is called when production cancelJobTask is exited.
func (s *BaseDorisParserListener) ExitCancelJobTask(ctx *CancelJobTaskContext) {}

// EnterAddConstraint is called when production addConstraint is entered.
func (s *BaseDorisParserListener) EnterAddConstraint(ctx *AddConstraintContext) {}

// ExitAddConstraint is called when production addConstraint is exited.
func (s *BaseDorisParserListener) ExitAddConstraint(ctx *AddConstraintContext) {}

// EnterDropConstraint is called when production dropConstraint is entered.
func (s *BaseDorisParserListener) EnterDropConstraint(ctx *DropConstraintContext) {}

// ExitDropConstraint is called when production dropConstraint is exited.
func (s *BaseDorisParserListener) ExitDropConstraint(ctx *DropConstraintContext) {}

// EnterShowConstraint is called when production showConstraint is entered.
func (s *BaseDorisParserListener) EnterShowConstraint(ctx *ShowConstraintContext) {}

// ExitShowConstraint is called when production showConstraint is exited.
func (s *BaseDorisParserListener) ExitShowConstraint(ctx *ShowConstraintContext) {}

// EnterInsertTable is called when production insertTable is entered.
func (s *BaseDorisParserListener) EnterInsertTable(ctx *InsertTableContext) {}

// ExitInsertTable is called when production insertTable is exited.
func (s *BaseDorisParserListener) ExitInsertTable(ctx *InsertTableContext) {}

// EnterUpdate is called when production update is entered.
func (s *BaseDorisParserListener) EnterUpdate(ctx *UpdateContext) {}

// ExitUpdate is called when production update is exited.
func (s *BaseDorisParserListener) ExitUpdate(ctx *UpdateContext) {}

// EnterDelete is called when production delete is entered.
func (s *BaseDorisParserListener) EnterDelete(ctx *DeleteContext) {}

// ExitDelete is called when production delete is exited.
func (s *BaseDorisParserListener) ExitDelete(ctx *DeleteContext) {}

// EnterLoad is called when production load is entered.
func (s *BaseDorisParserListener) EnterLoad(ctx *LoadContext) {}

// ExitLoad is called when production load is exited.
func (s *BaseDorisParserListener) ExitLoad(ctx *LoadContext) {}

// EnterExport is called when production export is entered.
func (s *BaseDorisParserListener) EnterExport(ctx *ExportContext) {}

// ExitExport is called when production export is exited.
func (s *BaseDorisParserListener) ExitExport(ctx *ExportContext) {}

// EnterReplay is called when production replay is entered.
func (s *BaseDorisParserListener) EnterReplay(ctx *ReplayContext) {}

// ExitReplay is called when production replay is exited.
func (s *BaseDorisParserListener) ExitReplay(ctx *ReplayContext) {}

// EnterCopyInto is called when production copyInto is entered.
func (s *BaseDorisParserListener) EnterCopyInto(ctx *CopyIntoContext) {}

// ExitCopyInto is called when production copyInto is exited.
func (s *BaseDorisParserListener) ExitCopyInto(ctx *CopyIntoContext) {}

// EnterTruncateTable is called when production truncateTable is entered.
func (s *BaseDorisParserListener) EnterTruncateTable(ctx *TruncateTableContext) {}

// ExitTruncateTable is called when production truncateTable is exited.
func (s *BaseDorisParserListener) ExitTruncateTable(ctx *TruncateTableContext) {}

// EnterCreateTable is called when production createTable is entered.
func (s *BaseDorisParserListener) EnterCreateTable(ctx *CreateTableContext) {}

// ExitCreateTable is called when production createTable is exited.
func (s *BaseDorisParserListener) ExitCreateTable(ctx *CreateTableContext) {}

// EnterCreateView is called when production createView is entered.
func (s *BaseDorisParserListener) EnterCreateView(ctx *CreateViewContext) {}

// ExitCreateView is called when production createView is exited.
func (s *BaseDorisParserListener) ExitCreateView(ctx *CreateViewContext) {}

// EnterCreateFile is called when production createFile is entered.
func (s *BaseDorisParserListener) EnterCreateFile(ctx *CreateFileContext) {}

// ExitCreateFile is called when production createFile is exited.
func (s *BaseDorisParserListener) ExitCreateFile(ctx *CreateFileContext) {}

// EnterCreateTableLike is called when production createTableLike is entered.
func (s *BaseDorisParserListener) EnterCreateTableLike(ctx *CreateTableLikeContext) {}

// ExitCreateTableLike is called when production createTableLike is exited.
func (s *BaseDorisParserListener) ExitCreateTableLike(ctx *CreateTableLikeContext) {}

// EnterCreateRole is called when production createRole is entered.
func (s *BaseDorisParserListener) EnterCreateRole(ctx *CreateRoleContext) {}

// ExitCreateRole is called when production createRole is exited.
func (s *BaseDorisParserListener) ExitCreateRole(ctx *CreateRoleContext) {}

// EnterCreateWorkloadGroup is called when production createWorkloadGroup is entered.
func (s *BaseDorisParserListener) EnterCreateWorkloadGroup(ctx *CreateWorkloadGroupContext) {}

// ExitCreateWorkloadGroup is called when production createWorkloadGroup is exited.
func (s *BaseDorisParserListener) ExitCreateWorkloadGroup(ctx *CreateWorkloadGroupContext) {}

// EnterCreateCatalog is called when production createCatalog is entered.
func (s *BaseDorisParserListener) EnterCreateCatalog(ctx *CreateCatalogContext) {}

// ExitCreateCatalog is called when production createCatalog is exited.
func (s *BaseDorisParserListener) ExitCreateCatalog(ctx *CreateCatalogContext) {}

// EnterCreateRowPolicy is called when production createRowPolicy is entered.
func (s *BaseDorisParserListener) EnterCreateRowPolicy(ctx *CreateRowPolicyContext) {}

// ExitCreateRowPolicy is called when production createRowPolicy is exited.
func (s *BaseDorisParserListener) ExitCreateRowPolicy(ctx *CreateRowPolicyContext) {}

// EnterCreateStoragePolicy is called when production createStoragePolicy is entered.
func (s *BaseDorisParserListener) EnterCreateStoragePolicy(ctx *CreateStoragePolicyContext) {}

// ExitCreateStoragePolicy is called when production createStoragePolicy is exited.
func (s *BaseDorisParserListener) ExitCreateStoragePolicy(ctx *CreateStoragePolicyContext) {}

// EnterBuildIndex is called when production buildIndex is entered.
func (s *BaseDorisParserListener) EnterBuildIndex(ctx *BuildIndexContext) {}

// ExitBuildIndex is called when production buildIndex is exited.
func (s *BaseDorisParserListener) ExitBuildIndex(ctx *BuildIndexContext) {}

// EnterCreateIndex is called when production createIndex is entered.
func (s *BaseDorisParserListener) EnterCreateIndex(ctx *CreateIndexContext) {}

// ExitCreateIndex is called when production createIndex is exited.
func (s *BaseDorisParserListener) ExitCreateIndex(ctx *CreateIndexContext) {}

// EnterCreateWorkloadPolicy is called when production createWorkloadPolicy is entered.
func (s *BaseDorisParserListener) EnterCreateWorkloadPolicy(ctx *CreateWorkloadPolicyContext) {}

// ExitCreateWorkloadPolicy is called when production createWorkloadPolicy is exited.
func (s *BaseDorisParserListener) ExitCreateWorkloadPolicy(ctx *CreateWorkloadPolicyContext) {}

// EnterCreateSqlBlockRule is called when production createSqlBlockRule is entered.
func (s *BaseDorisParserListener) EnterCreateSqlBlockRule(ctx *CreateSqlBlockRuleContext) {}

// ExitCreateSqlBlockRule is called when production createSqlBlockRule is exited.
func (s *BaseDorisParserListener) ExitCreateSqlBlockRule(ctx *CreateSqlBlockRuleContext) {}

// EnterCreateEncryptkey is called when production createEncryptkey is entered.
func (s *BaseDorisParserListener) EnterCreateEncryptkey(ctx *CreateEncryptkeyContext) {}

// ExitCreateEncryptkey is called when production createEncryptkey is exited.
func (s *BaseDorisParserListener) ExitCreateEncryptkey(ctx *CreateEncryptkeyContext) {}

// EnterCreateUserDefineFunction is called when production createUserDefineFunction is entered.
func (s *BaseDorisParserListener) EnterCreateUserDefineFunction(ctx *CreateUserDefineFunctionContext) {
}

// ExitCreateUserDefineFunction is called when production createUserDefineFunction is exited.
func (s *BaseDorisParserListener) ExitCreateUserDefineFunction(ctx *CreateUserDefineFunctionContext) {
}

// EnterCreateAliasFunction is called when production createAliasFunction is entered.
func (s *BaseDorisParserListener) EnterCreateAliasFunction(ctx *CreateAliasFunctionContext) {}

// ExitCreateAliasFunction is called when production createAliasFunction is exited.
func (s *BaseDorisParserListener) ExitCreateAliasFunction(ctx *CreateAliasFunctionContext) {}

// EnterCreateUser is called when production createUser is entered.
func (s *BaseDorisParserListener) EnterCreateUser(ctx *CreateUserContext) {}

// ExitCreateUser is called when production createUser is exited.
func (s *BaseDorisParserListener) ExitCreateUser(ctx *CreateUserContext) {}

// EnterCreateDatabase is called when production createDatabase is entered.
func (s *BaseDorisParserListener) EnterCreateDatabase(ctx *CreateDatabaseContext) {}

// ExitCreateDatabase is called when production createDatabase is exited.
func (s *BaseDorisParserListener) ExitCreateDatabase(ctx *CreateDatabaseContext) {}

// EnterCreateRepository is called when production createRepository is entered.
func (s *BaseDorisParserListener) EnterCreateRepository(ctx *CreateRepositoryContext) {}

// ExitCreateRepository is called when production createRepository is exited.
func (s *BaseDorisParserListener) ExitCreateRepository(ctx *CreateRepositoryContext) {}

// EnterCreateResource is called when production createResource is entered.
func (s *BaseDorisParserListener) EnterCreateResource(ctx *CreateResourceContext) {}

// ExitCreateResource is called when production createResource is exited.
func (s *BaseDorisParserListener) ExitCreateResource(ctx *CreateResourceContext) {}

// EnterCreateDictionary is called when production createDictionary is entered.
func (s *BaseDorisParserListener) EnterCreateDictionary(ctx *CreateDictionaryContext) {}

// ExitCreateDictionary is called when production createDictionary is exited.
func (s *BaseDorisParserListener) ExitCreateDictionary(ctx *CreateDictionaryContext) {}

// EnterCreateStage is called when production createStage is entered.
func (s *BaseDorisParserListener) EnterCreateStage(ctx *CreateStageContext) {}

// ExitCreateStage is called when production createStage is exited.
func (s *BaseDorisParserListener) ExitCreateStage(ctx *CreateStageContext) {}

// EnterCreateStorageVault is called when production createStorageVault is entered.
func (s *BaseDorisParserListener) EnterCreateStorageVault(ctx *CreateStorageVaultContext) {}

// ExitCreateStorageVault is called when production createStorageVault is exited.
func (s *BaseDorisParserListener) ExitCreateStorageVault(ctx *CreateStorageVaultContext) {}

// EnterCreateIndexAnalyzer is called when production createIndexAnalyzer is entered.
func (s *BaseDorisParserListener) EnterCreateIndexAnalyzer(ctx *CreateIndexAnalyzerContext) {}

// ExitCreateIndexAnalyzer is called when production createIndexAnalyzer is exited.
func (s *BaseDorisParserListener) ExitCreateIndexAnalyzer(ctx *CreateIndexAnalyzerContext) {}

// EnterCreateIndexTokenizer is called when production createIndexTokenizer is entered.
func (s *BaseDorisParserListener) EnterCreateIndexTokenizer(ctx *CreateIndexTokenizerContext) {}

// ExitCreateIndexTokenizer is called when production createIndexTokenizer is exited.
func (s *BaseDorisParserListener) ExitCreateIndexTokenizer(ctx *CreateIndexTokenizerContext) {}

// EnterCreateIndexTokenFilter is called when production createIndexTokenFilter is entered.
func (s *BaseDorisParserListener) EnterCreateIndexTokenFilter(ctx *CreateIndexTokenFilterContext) {}

// ExitCreateIndexTokenFilter is called when production createIndexTokenFilter is exited.
func (s *BaseDorisParserListener) ExitCreateIndexTokenFilter(ctx *CreateIndexTokenFilterContext) {}

// EnterDictionaryColumnDefs is called when production dictionaryColumnDefs is entered.
func (s *BaseDorisParserListener) EnterDictionaryColumnDefs(ctx *DictionaryColumnDefsContext) {}

// ExitDictionaryColumnDefs is called when production dictionaryColumnDefs is exited.
func (s *BaseDorisParserListener) ExitDictionaryColumnDefs(ctx *DictionaryColumnDefsContext) {}

// EnterDictionaryColumnDef is called when production dictionaryColumnDef is entered.
func (s *BaseDorisParserListener) EnterDictionaryColumnDef(ctx *DictionaryColumnDefContext) {}

// ExitDictionaryColumnDef is called when production dictionaryColumnDef is exited.
func (s *BaseDorisParserListener) ExitDictionaryColumnDef(ctx *DictionaryColumnDefContext) {}

// EnterAlterSystem is called when production alterSystem is entered.
func (s *BaseDorisParserListener) EnterAlterSystem(ctx *AlterSystemContext) {}

// ExitAlterSystem is called when production alterSystem is exited.
func (s *BaseDorisParserListener) ExitAlterSystem(ctx *AlterSystemContext) {}

// EnterAlterView is called when production alterView is entered.
func (s *BaseDorisParserListener) EnterAlterView(ctx *AlterViewContext) {}

// ExitAlterView is called when production alterView is exited.
func (s *BaseDorisParserListener) ExitAlterView(ctx *AlterViewContext) {}

// EnterAlterCatalogRename is called when production alterCatalogRename is entered.
func (s *BaseDorisParserListener) EnterAlterCatalogRename(ctx *AlterCatalogRenameContext) {}

// ExitAlterCatalogRename is called when production alterCatalogRename is exited.
func (s *BaseDorisParserListener) ExitAlterCatalogRename(ctx *AlterCatalogRenameContext) {}

// EnterAlterRole is called when production alterRole is entered.
func (s *BaseDorisParserListener) EnterAlterRole(ctx *AlterRoleContext) {}

// ExitAlterRole is called when production alterRole is exited.
func (s *BaseDorisParserListener) ExitAlterRole(ctx *AlterRoleContext) {}

// EnterAlterStorageVault is called when production alterStorageVault is entered.
func (s *BaseDorisParserListener) EnterAlterStorageVault(ctx *AlterStorageVaultContext) {}

// ExitAlterStorageVault is called when production alterStorageVault is exited.
func (s *BaseDorisParserListener) ExitAlterStorageVault(ctx *AlterStorageVaultContext) {}

// EnterAlterWorkloadGroup is called when production alterWorkloadGroup is entered.
func (s *BaseDorisParserListener) EnterAlterWorkloadGroup(ctx *AlterWorkloadGroupContext) {}

// ExitAlterWorkloadGroup is called when production alterWorkloadGroup is exited.
func (s *BaseDorisParserListener) ExitAlterWorkloadGroup(ctx *AlterWorkloadGroupContext) {}

// EnterAlterCatalogProperties is called when production alterCatalogProperties is entered.
func (s *BaseDorisParserListener) EnterAlterCatalogProperties(ctx *AlterCatalogPropertiesContext) {}

// ExitAlterCatalogProperties is called when production alterCatalogProperties is exited.
func (s *BaseDorisParserListener) ExitAlterCatalogProperties(ctx *AlterCatalogPropertiesContext) {}

// EnterAlterWorkloadPolicy is called when production alterWorkloadPolicy is entered.
func (s *BaseDorisParserListener) EnterAlterWorkloadPolicy(ctx *AlterWorkloadPolicyContext) {}

// ExitAlterWorkloadPolicy is called when production alterWorkloadPolicy is exited.
func (s *BaseDorisParserListener) ExitAlterWorkloadPolicy(ctx *AlterWorkloadPolicyContext) {}

// EnterAlterSqlBlockRule is called when production alterSqlBlockRule is entered.
func (s *BaseDorisParserListener) EnterAlterSqlBlockRule(ctx *AlterSqlBlockRuleContext) {}

// ExitAlterSqlBlockRule is called when production alterSqlBlockRule is exited.
func (s *BaseDorisParserListener) ExitAlterSqlBlockRule(ctx *AlterSqlBlockRuleContext) {}

// EnterAlterCatalogComment is called when production alterCatalogComment is entered.
func (s *BaseDorisParserListener) EnterAlterCatalogComment(ctx *AlterCatalogCommentContext) {}

// ExitAlterCatalogComment is called when production alterCatalogComment is exited.
func (s *BaseDorisParserListener) ExitAlterCatalogComment(ctx *AlterCatalogCommentContext) {}

// EnterAlterDatabaseRename is called when production alterDatabaseRename is entered.
func (s *BaseDorisParserListener) EnterAlterDatabaseRename(ctx *AlterDatabaseRenameContext) {}

// ExitAlterDatabaseRename is called when production alterDatabaseRename is exited.
func (s *BaseDorisParserListener) ExitAlterDatabaseRename(ctx *AlterDatabaseRenameContext) {}

// EnterAlterStoragePolicy is called when production alterStoragePolicy is entered.
func (s *BaseDorisParserListener) EnterAlterStoragePolicy(ctx *AlterStoragePolicyContext) {}

// ExitAlterStoragePolicy is called when production alterStoragePolicy is exited.
func (s *BaseDorisParserListener) ExitAlterStoragePolicy(ctx *AlterStoragePolicyContext) {}

// EnterAlterTable is called when production alterTable is entered.
func (s *BaseDorisParserListener) EnterAlterTable(ctx *AlterTableContext) {}

// ExitAlterTable is called when production alterTable is exited.
func (s *BaseDorisParserListener) ExitAlterTable(ctx *AlterTableContext) {}

// EnterAlterTableAddRollup is called when production alterTableAddRollup is entered.
func (s *BaseDorisParserListener) EnterAlterTableAddRollup(ctx *AlterTableAddRollupContext) {}

// ExitAlterTableAddRollup is called when production alterTableAddRollup is exited.
func (s *BaseDorisParserListener) ExitAlterTableAddRollup(ctx *AlterTableAddRollupContext) {}

// EnterAlterTableDropRollup is called when production alterTableDropRollup is entered.
func (s *BaseDorisParserListener) EnterAlterTableDropRollup(ctx *AlterTableDropRollupContext) {}

// ExitAlterTableDropRollup is called when production alterTableDropRollup is exited.
func (s *BaseDorisParserListener) ExitAlterTableDropRollup(ctx *AlterTableDropRollupContext) {}

// EnterAlterTableProperties is called when production alterTableProperties is entered.
func (s *BaseDorisParserListener) EnterAlterTableProperties(ctx *AlterTablePropertiesContext) {}

// ExitAlterTableProperties is called when production alterTableProperties is exited.
func (s *BaseDorisParserListener) ExitAlterTableProperties(ctx *AlterTablePropertiesContext) {}

// EnterAlterDatabaseSetQuota is called when production alterDatabaseSetQuota is entered.
func (s *BaseDorisParserListener) EnterAlterDatabaseSetQuota(ctx *AlterDatabaseSetQuotaContext) {}

// ExitAlterDatabaseSetQuota is called when production alterDatabaseSetQuota is exited.
func (s *BaseDorisParserListener) ExitAlterDatabaseSetQuota(ctx *AlterDatabaseSetQuotaContext) {}

// EnterAlterDatabaseProperties is called when production alterDatabaseProperties is entered.
func (s *BaseDorisParserListener) EnterAlterDatabaseProperties(ctx *AlterDatabasePropertiesContext) {}

// ExitAlterDatabaseProperties is called when production alterDatabaseProperties is exited.
func (s *BaseDorisParserListener) ExitAlterDatabaseProperties(ctx *AlterDatabasePropertiesContext) {}

// EnterAlterSystemRenameComputeGroup is called when production alterSystemRenameComputeGroup is entered.
func (s *BaseDorisParserListener) EnterAlterSystemRenameComputeGroup(ctx *AlterSystemRenameComputeGroupContext) {
}

// ExitAlterSystemRenameComputeGroup is called when production alterSystemRenameComputeGroup is exited.
func (s *BaseDorisParserListener) ExitAlterSystemRenameComputeGroup(ctx *AlterSystemRenameComputeGroupContext) {
}

// EnterAlterResource is called when production alterResource is entered.
func (s *BaseDorisParserListener) EnterAlterResource(ctx *AlterResourceContext) {}

// ExitAlterResource is called when production alterResource is exited.
func (s *BaseDorisParserListener) ExitAlterResource(ctx *AlterResourceContext) {}

// EnterAlterRepository is called when production alterRepository is entered.
func (s *BaseDorisParserListener) EnterAlterRepository(ctx *AlterRepositoryContext) {}

// ExitAlterRepository is called when production alterRepository is exited.
func (s *BaseDorisParserListener) ExitAlterRepository(ctx *AlterRepositoryContext) {}

// EnterAlterRoutineLoad is called when production alterRoutineLoad is entered.
func (s *BaseDorisParserListener) EnterAlterRoutineLoad(ctx *AlterRoutineLoadContext) {}

// ExitAlterRoutineLoad is called when production alterRoutineLoad is exited.
func (s *BaseDorisParserListener) ExitAlterRoutineLoad(ctx *AlterRoutineLoadContext) {}

// EnterAlterColocateGroup is called when production alterColocateGroup is entered.
func (s *BaseDorisParserListener) EnterAlterColocateGroup(ctx *AlterColocateGroupContext) {}

// ExitAlterColocateGroup is called when production alterColocateGroup is exited.
func (s *BaseDorisParserListener) ExitAlterColocateGroup(ctx *AlterColocateGroupContext) {}

// EnterAlterUser is called when production alterUser is entered.
func (s *BaseDorisParserListener) EnterAlterUser(ctx *AlterUserContext) {}

// ExitAlterUser is called when production alterUser is exited.
func (s *BaseDorisParserListener) ExitAlterUser(ctx *AlterUserContext) {}

// EnterDropCatalogRecycleBin is called when production dropCatalogRecycleBin is entered.
func (s *BaseDorisParserListener) EnterDropCatalogRecycleBin(ctx *DropCatalogRecycleBinContext) {}

// ExitDropCatalogRecycleBin is called when production dropCatalogRecycleBin is exited.
func (s *BaseDorisParserListener) ExitDropCatalogRecycleBin(ctx *DropCatalogRecycleBinContext) {}

// EnterDropEncryptkey is called when production dropEncryptkey is entered.
func (s *BaseDorisParserListener) EnterDropEncryptkey(ctx *DropEncryptkeyContext) {}

// ExitDropEncryptkey is called when production dropEncryptkey is exited.
func (s *BaseDorisParserListener) ExitDropEncryptkey(ctx *DropEncryptkeyContext) {}

// EnterDropRole is called when production dropRole is entered.
func (s *BaseDorisParserListener) EnterDropRole(ctx *DropRoleContext) {}

// ExitDropRole is called when production dropRole is exited.
func (s *BaseDorisParserListener) ExitDropRole(ctx *DropRoleContext) {}

// EnterDropSqlBlockRule is called when production dropSqlBlockRule is entered.
func (s *BaseDorisParserListener) EnterDropSqlBlockRule(ctx *DropSqlBlockRuleContext) {}

// ExitDropSqlBlockRule is called when production dropSqlBlockRule is exited.
func (s *BaseDorisParserListener) ExitDropSqlBlockRule(ctx *DropSqlBlockRuleContext) {}

// EnterDropUser is called when production dropUser is entered.
func (s *BaseDorisParserListener) EnterDropUser(ctx *DropUserContext) {}

// ExitDropUser is called when production dropUser is exited.
func (s *BaseDorisParserListener) ExitDropUser(ctx *DropUserContext) {}

// EnterDropStoragePolicy is called when production dropStoragePolicy is entered.
func (s *BaseDorisParserListener) EnterDropStoragePolicy(ctx *DropStoragePolicyContext) {}

// ExitDropStoragePolicy is called when production dropStoragePolicy is exited.
func (s *BaseDorisParserListener) ExitDropStoragePolicy(ctx *DropStoragePolicyContext) {}

// EnterDropWorkloadGroup is called when production dropWorkloadGroup is entered.
func (s *BaseDorisParserListener) EnterDropWorkloadGroup(ctx *DropWorkloadGroupContext) {}

// ExitDropWorkloadGroup is called when production dropWorkloadGroup is exited.
func (s *BaseDorisParserListener) ExitDropWorkloadGroup(ctx *DropWorkloadGroupContext) {}

// EnterDropCatalog is called when production dropCatalog is entered.
func (s *BaseDorisParserListener) EnterDropCatalog(ctx *DropCatalogContext) {}

// ExitDropCatalog is called when production dropCatalog is exited.
func (s *BaseDorisParserListener) ExitDropCatalog(ctx *DropCatalogContext) {}

// EnterDropFile is called when production dropFile is entered.
func (s *BaseDorisParserListener) EnterDropFile(ctx *DropFileContext) {}

// ExitDropFile is called when production dropFile is exited.
func (s *BaseDorisParserListener) ExitDropFile(ctx *DropFileContext) {}

// EnterDropWorkloadPolicy is called when production dropWorkloadPolicy is entered.
func (s *BaseDorisParserListener) EnterDropWorkloadPolicy(ctx *DropWorkloadPolicyContext) {}

// ExitDropWorkloadPolicy is called when production dropWorkloadPolicy is exited.
func (s *BaseDorisParserListener) ExitDropWorkloadPolicy(ctx *DropWorkloadPolicyContext) {}

// EnterDropRepository is called when production dropRepository is entered.
func (s *BaseDorisParserListener) EnterDropRepository(ctx *DropRepositoryContext) {}

// ExitDropRepository is called when production dropRepository is exited.
func (s *BaseDorisParserListener) ExitDropRepository(ctx *DropRepositoryContext) {}

// EnterDropTable is called when production dropTable is entered.
func (s *BaseDorisParserListener) EnterDropTable(ctx *DropTableContext) {}

// ExitDropTable is called when production dropTable is exited.
func (s *BaseDorisParserListener) ExitDropTable(ctx *DropTableContext) {}

// EnterDropDatabase is called when production dropDatabase is entered.
func (s *BaseDorisParserListener) EnterDropDatabase(ctx *DropDatabaseContext) {}

// ExitDropDatabase is called when production dropDatabase is exited.
func (s *BaseDorisParserListener) ExitDropDatabase(ctx *DropDatabaseContext) {}

// EnterDropFunction is called when production dropFunction is entered.
func (s *BaseDorisParserListener) EnterDropFunction(ctx *DropFunctionContext) {}

// ExitDropFunction is called when production dropFunction is exited.
func (s *BaseDorisParserListener) ExitDropFunction(ctx *DropFunctionContext) {}

// EnterDropIndex is called when production dropIndex is entered.
func (s *BaseDorisParserListener) EnterDropIndex(ctx *DropIndexContext) {}

// ExitDropIndex is called when production dropIndex is exited.
func (s *BaseDorisParserListener) ExitDropIndex(ctx *DropIndexContext) {}

// EnterDropResource is called when production dropResource is entered.
func (s *BaseDorisParserListener) EnterDropResource(ctx *DropResourceContext) {}

// ExitDropResource is called when production dropResource is exited.
func (s *BaseDorisParserListener) ExitDropResource(ctx *DropResourceContext) {}

// EnterDropRowPolicy is called when production dropRowPolicy is entered.
func (s *BaseDorisParserListener) EnterDropRowPolicy(ctx *DropRowPolicyContext) {}

// ExitDropRowPolicy is called when production dropRowPolicy is exited.
func (s *BaseDorisParserListener) ExitDropRowPolicy(ctx *DropRowPolicyContext) {}

// EnterDropDictionary is called when production dropDictionary is entered.
func (s *BaseDorisParserListener) EnterDropDictionary(ctx *DropDictionaryContext) {}

// ExitDropDictionary is called when production dropDictionary is exited.
func (s *BaseDorisParserListener) ExitDropDictionary(ctx *DropDictionaryContext) {}

// EnterDropStage is called when production dropStage is entered.
func (s *BaseDorisParserListener) EnterDropStage(ctx *DropStageContext) {}

// ExitDropStage is called when production dropStage is exited.
func (s *BaseDorisParserListener) ExitDropStage(ctx *DropStageContext) {}

// EnterDropView is called when production dropView is entered.
func (s *BaseDorisParserListener) EnterDropView(ctx *DropViewContext) {}

// ExitDropView is called when production dropView is exited.
func (s *BaseDorisParserListener) ExitDropView(ctx *DropViewContext) {}

// EnterDropIndexAnalyzer is called when production dropIndexAnalyzer is entered.
func (s *BaseDorisParserListener) EnterDropIndexAnalyzer(ctx *DropIndexAnalyzerContext) {}

// ExitDropIndexAnalyzer is called when production dropIndexAnalyzer is exited.
func (s *BaseDorisParserListener) ExitDropIndexAnalyzer(ctx *DropIndexAnalyzerContext) {}

// EnterDropIndexTokenizer is called when production dropIndexTokenizer is entered.
func (s *BaseDorisParserListener) EnterDropIndexTokenizer(ctx *DropIndexTokenizerContext) {}

// ExitDropIndexTokenizer is called when production dropIndexTokenizer is exited.
func (s *BaseDorisParserListener) ExitDropIndexTokenizer(ctx *DropIndexTokenizerContext) {}

// EnterDropIndexTokenFilter is called when production dropIndexTokenFilter is entered.
func (s *BaseDorisParserListener) EnterDropIndexTokenFilter(ctx *DropIndexTokenFilterContext) {}

// ExitDropIndexTokenFilter is called when production dropIndexTokenFilter is exited.
func (s *BaseDorisParserListener) ExitDropIndexTokenFilter(ctx *DropIndexTokenFilterContext) {}

// EnterShowVariables is called when production showVariables is entered.
func (s *BaseDorisParserListener) EnterShowVariables(ctx *ShowVariablesContext) {}

// ExitShowVariables is called when production showVariables is exited.
func (s *BaseDorisParserListener) ExitShowVariables(ctx *ShowVariablesContext) {}

// EnterShowAuthors is called when production showAuthors is entered.
func (s *BaseDorisParserListener) EnterShowAuthors(ctx *ShowAuthorsContext) {}

// ExitShowAuthors is called when production showAuthors is exited.
func (s *BaseDorisParserListener) ExitShowAuthors(ctx *ShowAuthorsContext) {}

// EnterShowAlterTable is called when production showAlterTable is entered.
func (s *BaseDorisParserListener) EnterShowAlterTable(ctx *ShowAlterTableContext) {}

// ExitShowAlterTable is called when production showAlterTable is exited.
func (s *BaseDorisParserListener) ExitShowAlterTable(ctx *ShowAlterTableContext) {}

// EnterShowCreateDatabase is called when production showCreateDatabase is entered.
func (s *BaseDorisParserListener) EnterShowCreateDatabase(ctx *ShowCreateDatabaseContext) {}

// ExitShowCreateDatabase is called when production showCreateDatabase is exited.
func (s *BaseDorisParserListener) ExitShowCreateDatabase(ctx *ShowCreateDatabaseContext) {}

// EnterShowBackup is called when production showBackup is entered.
func (s *BaseDorisParserListener) EnterShowBackup(ctx *ShowBackupContext) {}

// ExitShowBackup is called when production showBackup is exited.
func (s *BaseDorisParserListener) ExitShowBackup(ctx *ShowBackupContext) {}

// EnterShowBroker is called when production showBroker is entered.
func (s *BaseDorisParserListener) EnterShowBroker(ctx *ShowBrokerContext) {}

// ExitShowBroker is called when production showBroker is exited.
func (s *BaseDorisParserListener) ExitShowBroker(ctx *ShowBrokerContext) {}

// EnterShowBuildIndex is called when production showBuildIndex is entered.
func (s *BaseDorisParserListener) EnterShowBuildIndex(ctx *ShowBuildIndexContext) {}

// ExitShowBuildIndex is called when production showBuildIndex is exited.
func (s *BaseDorisParserListener) ExitShowBuildIndex(ctx *ShowBuildIndexContext) {}

// EnterShowDynamicPartition is called when production showDynamicPartition is entered.
func (s *BaseDorisParserListener) EnterShowDynamicPartition(ctx *ShowDynamicPartitionContext) {}

// ExitShowDynamicPartition is called when production showDynamicPartition is exited.
func (s *BaseDorisParserListener) ExitShowDynamicPartition(ctx *ShowDynamicPartitionContext) {}

// EnterShowEvents is called when production showEvents is entered.
func (s *BaseDorisParserListener) EnterShowEvents(ctx *ShowEventsContext) {}

// ExitShowEvents is called when production showEvents is exited.
func (s *BaseDorisParserListener) ExitShowEvents(ctx *ShowEventsContext) {}

// EnterShowExport is called when production showExport is entered.
func (s *BaseDorisParserListener) EnterShowExport(ctx *ShowExportContext) {}

// ExitShowExport is called when production showExport is exited.
func (s *BaseDorisParserListener) ExitShowExport(ctx *ShowExportContext) {}

// EnterShowLastInsert is called when production showLastInsert is entered.
func (s *BaseDorisParserListener) EnterShowLastInsert(ctx *ShowLastInsertContext) {}

// ExitShowLastInsert is called when production showLastInsert is exited.
func (s *BaseDorisParserListener) ExitShowLastInsert(ctx *ShowLastInsertContext) {}

// EnterShowCharset is called when production showCharset is entered.
func (s *BaseDorisParserListener) EnterShowCharset(ctx *ShowCharsetContext) {}

// ExitShowCharset is called when production showCharset is exited.
func (s *BaseDorisParserListener) ExitShowCharset(ctx *ShowCharsetContext) {}

// EnterShowDelete is called when production showDelete is entered.
func (s *BaseDorisParserListener) EnterShowDelete(ctx *ShowDeleteContext) {}

// ExitShowDelete is called when production showDelete is exited.
func (s *BaseDorisParserListener) ExitShowDelete(ctx *ShowDeleteContext) {}

// EnterShowCreateFunction is called when production showCreateFunction is entered.
func (s *BaseDorisParserListener) EnterShowCreateFunction(ctx *ShowCreateFunctionContext) {}

// ExitShowCreateFunction is called when production showCreateFunction is exited.
func (s *BaseDorisParserListener) ExitShowCreateFunction(ctx *ShowCreateFunctionContext) {}

// EnterShowFunctions is called when production showFunctions is entered.
func (s *BaseDorisParserListener) EnterShowFunctions(ctx *ShowFunctionsContext) {}

// ExitShowFunctions is called when production showFunctions is exited.
func (s *BaseDorisParserListener) ExitShowFunctions(ctx *ShowFunctionsContext) {}

// EnterShowGlobalFunctions is called when production showGlobalFunctions is entered.
func (s *BaseDorisParserListener) EnterShowGlobalFunctions(ctx *ShowGlobalFunctionsContext) {}

// ExitShowGlobalFunctions is called when production showGlobalFunctions is exited.
func (s *BaseDorisParserListener) ExitShowGlobalFunctions(ctx *ShowGlobalFunctionsContext) {}

// EnterShowGrants is called when production showGrants is entered.
func (s *BaseDorisParserListener) EnterShowGrants(ctx *ShowGrantsContext) {}

// ExitShowGrants is called when production showGrants is exited.
func (s *BaseDorisParserListener) ExitShowGrants(ctx *ShowGrantsContext) {}

// EnterShowGrantsForUser is called when production showGrantsForUser is entered.
func (s *BaseDorisParserListener) EnterShowGrantsForUser(ctx *ShowGrantsForUserContext) {}

// ExitShowGrantsForUser is called when production showGrantsForUser is exited.
func (s *BaseDorisParserListener) ExitShowGrantsForUser(ctx *ShowGrantsForUserContext) {}

// EnterShowCreateUser is called when production showCreateUser is entered.
func (s *BaseDorisParserListener) EnterShowCreateUser(ctx *ShowCreateUserContext) {}

// ExitShowCreateUser is called when production showCreateUser is exited.
func (s *BaseDorisParserListener) ExitShowCreateUser(ctx *ShowCreateUserContext) {}

// EnterShowSnapshot is called when production showSnapshot is entered.
func (s *BaseDorisParserListener) EnterShowSnapshot(ctx *ShowSnapshotContext) {}

// ExitShowSnapshot is called when production showSnapshot is exited.
func (s *BaseDorisParserListener) ExitShowSnapshot(ctx *ShowSnapshotContext) {}

// EnterShowLoadProfile is called when production showLoadProfile is entered.
func (s *BaseDorisParserListener) EnterShowLoadProfile(ctx *ShowLoadProfileContext) {}

// ExitShowLoadProfile is called when production showLoadProfile is exited.
func (s *BaseDorisParserListener) ExitShowLoadProfile(ctx *ShowLoadProfileContext) {}

// EnterShowCreateRepository is called when production showCreateRepository is entered.
func (s *BaseDorisParserListener) EnterShowCreateRepository(ctx *ShowCreateRepositoryContext) {}

// ExitShowCreateRepository is called when production showCreateRepository is exited.
func (s *BaseDorisParserListener) ExitShowCreateRepository(ctx *ShowCreateRepositoryContext) {}

// EnterShowView is called when production showView is entered.
func (s *BaseDorisParserListener) EnterShowView(ctx *ShowViewContext) {}

// ExitShowView is called when production showView is exited.
func (s *BaseDorisParserListener) ExitShowView(ctx *ShowViewContext) {}

// EnterShowPlugins is called when production showPlugins is entered.
func (s *BaseDorisParserListener) EnterShowPlugins(ctx *ShowPluginsContext) {}

// ExitShowPlugins is called when production showPlugins is exited.
func (s *BaseDorisParserListener) ExitShowPlugins(ctx *ShowPluginsContext) {}

// EnterShowStorageVault is called when production showStorageVault is entered.
func (s *BaseDorisParserListener) EnterShowStorageVault(ctx *ShowStorageVaultContext) {}

// ExitShowStorageVault is called when production showStorageVault is exited.
func (s *BaseDorisParserListener) ExitShowStorageVault(ctx *ShowStorageVaultContext) {}

// EnterShowRepositories is called when production showRepositories is entered.
func (s *BaseDorisParserListener) EnterShowRepositories(ctx *ShowRepositoriesContext) {}

// ExitShowRepositories is called when production showRepositories is exited.
func (s *BaseDorisParserListener) ExitShowRepositories(ctx *ShowRepositoriesContext) {}

// EnterShowEncryptKeys is called when production showEncryptKeys is entered.
func (s *BaseDorisParserListener) EnterShowEncryptKeys(ctx *ShowEncryptKeysContext) {}

// ExitShowEncryptKeys is called when production showEncryptKeys is exited.
func (s *BaseDorisParserListener) ExitShowEncryptKeys(ctx *ShowEncryptKeysContext) {}

// EnterShowCreateTable is called when production showCreateTable is entered.
func (s *BaseDorisParserListener) EnterShowCreateTable(ctx *ShowCreateTableContext) {}

// ExitShowCreateTable is called when production showCreateTable is exited.
func (s *BaseDorisParserListener) ExitShowCreateTable(ctx *ShowCreateTableContext) {}

// EnterShowProcessList is called when production showProcessList is entered.
func (s *BaseDorisParserListener) EnterShowProcessList(ctx *ShowProcessListContext) {}

// ExitShowProcessList is called when production showProcessList is exited.
func (s *BaseDorisParserListener) ExitShowProcessList(ctx *ShowProcessListContext) {}

// EnterShowPartitions is called when production showPartitions is entered.
func (s *BaseDorisParserListener) EnterShowPartitions(ctx *ShowPartitionsContext) {}

// ExitShowPartitions is called when production showPartitions is exited.
func (s *BaseDorisParserListener) ExitShowPartitions(ctx *ShowPartitionsContext) {}

// EnterShowRestore is called when production showRestore is entered.
func (s *BaseDorisParserListener) EnterShowRestore(ctx *ShowRestoreContext) {}

// ExitShowRestore is called when production showRestore is exited.
func (s *BaseDorisParserListener) ExitShowRestore(ctx *ShowRestoreContext) {}

// EnterShowRoles is called when production showRoles is entered.
func (s *BaseDorisParserListener) EnterShowRoles(ctx *ShowRolesContext) {}

// ExitShowRoles is called when production showRoles is exited.
func (s *BaseDorisParserListener) ExitShowRoles(ctx *ShowRolesContext) {}

// EnterShowPartitionId is called when production showPartitionId is entered.
func (s *BaseDorisParserListener) EnterShowPartitionId(ctx *ShowPartitionIdContext) {}

// ExitShowPartitionId is called when production showPartitionId is exited.
func (s *BaseDorisParserListener) ExitShowPartitionId(ctx *ShowPartitionIdContext) {}

// EnterShowPrivileges is called when production showPrivileges is entered.
func (s *BaseDorisParserListener) EnterShowPrivileges(ctx *ShowPrivilegesContext) {}

// ExitShowPrivileges is called when production showPrivileges is exited.
func (s *BaseDorisParserListener) ExitShowPrivileges(ctx *ShowPrivilegesContext) {}

// EnterShowProc is called when production showProc is entered.
func (s *BaseDorisParserListener) EnterShowProc(ctx *ShowProcContext) {}

// ExitShowProc is called when production showProc is exited.
func (s *BaseDorisParserListener) ExitShowProc(ctx *ShowProcContext) {}

// EnterShowSmallFiles is called when production showSmallFiles is entered.
func (s *BaseDorisParserListener) EnterShowSmallFiles(ctx *ShowSmallFilesContext) {}

// ExitShowSmallFiles is called when production showSmallFiles is exited.
func (s *BaseDorisParserListener) ExitShowSmallFiles(ctx *ShowSmallFilesContext) {}

// EnterShowStorageEngines is called when production showStorageEngines is entered.
func (s *BaseDorisParserListener) EnterShowStorageEngines(ctx *ShowStorageEnginesContext) {}

// ExitShowStorageEngines is called when production showStorageEngines is exited.
func (s *BaseDorisParserListener) ExitShowStorageEngines(ctx *ShowStorageEnginesContext) {}

// EnterShowCreateCatalog is called when production showCreateCatalog is entered.
func (s *BaseDorisParserListener) EnterShowCreateCatalog(ctx *ShowCreateCatalogContext) {}

// ExitShowCreateCatalog is called when production showCreateCatalog is exited.
func (s *BaseDorisParserListener) ExitShowCreateCatalog(ctx *ShowCreateCatalogContext) {}

// EnterShowCatalog is called when production showCatalog is entered.
func (s *BaseDorisParserListener) EnterShowCatalog(ctx *ShowCatalogContext) {}

// ExitShowCatalog is called when production showCatalog is exited.
func (s *BaseDorisParserListener) ExitShowCatalog(ctx *ShowCatalogContext) {}

// EnterShowCatalogs is called when production showCatalogs is entered.
func (s *BaseDorisParserListener) EnterShowCatalogs(ctx *ShowCatalogsContext) {}

// ExitShowCatalogs is called when production showCatalogs is exited.
func (s *BaseDorisParserListener) ExitShowCatalogs(ctx *ShowCatalogsContext) {}

// EnterShowUserProperties is called when production showUserProperties is entered.
func (s *BaseDorisParserListener) EnterShowUserProperties(ctx *ShowUserPropertiesContext) {}

// ExitShowUserProperties is called when production showUserProperties is exited.
func (s *BaseDorisParserListener) ExitShowUserProperties(ctx *ShowUserPropertiesContext) {}

// EnterShowAllProperties is called when production showAllProperties is entered.
func (s *BaseDorisParserListener) EnterShowAllProperties(ctx *ShowAllPropertiesContext) {}

// ExitShowAllProperties is called when production showAllProperties is exited.
func (s *BaseDorisParserListener) ExitShowAllProperties(ctx *ShowAllPropertiesContext) {}

// EnterShowCollation is called when production showCollation is entered.
func (s *BaseDorisParserListener) EnterShowCollation(ctx *ShowCollationContext) {}

// ExitShowCollation is called when production showCollation is exited.
func (s *BaseDorisParserListener) ExitShowCollation(ctx *ShowCollationContext) {}

// EnterShowRowPolicy is called when production showRowPolicy is entered.
func (s *BaseDorisParserListener) EnterShowRowPolicy(ctx *ShowRowPolicyContext) {}

// ExitShowRowPolicy is called when production showRowPolicy is exited.
func (s *BaseDorisParserListener) ExitShowRowPolicy(ctx *ShowRowPolicyContext) {}

// EnterShowStoragePolicy is called when production showStoragePolicy is entered.
func (s *BaseDorisParserListener) EnterShowStoragePolicy(ctx *ShowStoragePolicyContext) {}

// ExitShowStoragePolicy is called when production showStoragePolicy is exited.
func (s *BaseDorisParserListener) ExitShowStoragePolicy(ctx *ShowStoragePolicyContext) {}

// EnterShowSqlBlockRule is called when production showSqlBlockRule is entered.
func (s *BaseDorisParserListener) EnterShowSqlBlockRule(ctx *ShowSqlBlockRuleContext) {}

// ExitShowSqlBlockRule is called when production showSqlBlockRule is exited.
func (s *BaseDorisParserListener) ExitShowSqlBlockRule(ctx *ShowSqlBlockRuleContext) {}

// EnterShowCreateView is called when production showCreateView is entered.
func (s *BaseDorisParserListener) EnterShowCreateView(ctx *ShowCreateViewContext) {}

// ExitShowCreateView is called when production showCreateView is exited.
func (s *BaseDorisParserListener) ExitShowCreateView(ctx *ShowCreateViewContext) {}

// EnterShowDataTypes is called when production showDataTypes is entered.
func (s *BaseDorisParserListener) EnterShowDataTypes(ctx *ShowDataTypesContext) {}

// ExitShowDataTypes is called when production showDataTypes is exited.
func (s *BaseDorisParserListener) ExitShowDataTypes(ctx *ShowDataTypesContext) {}

// EnterShowData is called when production showData is entered.
func (s *BaseDorisParserListener) EnterShowData(ctx *ShowDataContext) {}

// ExitShowData is called when production showData is exited.
func (s *BaseDorisParserListener) ExitShowData(ctx *ShowDataContext) {}

// EnterShowCreateMaterializedView is called when production showCreateMaterializedView is entered.
func (s *BaseDorisParserListener) EnterShowCreateMaterializedView(ctx *ShowCreateMaterializedViewContext) {
}

// ExitShowCreateMaterializedView is called when production showCreateMaterializedView is exited.
func (s *BaseDorisParserListener) ExitShowCreateMaterializedView(ctx *ShowCreateMaterializedViewContext) {
}

// EnterShowWarningErrors is called when production showWarningErrors is entered.
func (s *BaseDorisParserListener) EnterShowWarningErrors(ctx *ShowWarningErrorsContext) {}

// ExitShowWarningErrors is called when production showWarningErrors is exited.
func (s *BaseDorisParserListener) ExitShowWarningErrors(ctx *ShowWarningErrorsContext) {}

// EnterShowWarningErrorCount is called when production showWarningErrorCount is entered.
func (s *BaseDorisParserListener) EnterShowWarningErrorCount(ctx *ShowWarningErrorCountContext) {}

// ExitShowWarningErrorCount is called when production showWarningErrorCount is exited.
func (s *BaseDorisParserListener) ExitShowWarningErrorCount(ctx *ShowWarningErrorCountContext) {}

// EnterShowBackends is called when production showBackends is entered.
func (s *BaseDorisParserListener) EnterShowBackends(ctx *ShowBackendsContext) {}

// ExitShowBackends is called when production showBackends is exited.
func (s *BaseDorisParserListener) ExitShowBackends(ctx *ShowBackendsContext) {}

// EnterShowStages is called when production showStages is entered.
func (s *BaseDorisParserListener) EnterShowStages(ctx *ShowStagesContext) {}

// ExitShowStages is called when production showStages is exited.
func (s *BaseDorisParserListener) ExitShowStages(ctx *ShowStagesContext) {}

// EnterShowReplicaDistribution is called when production showReplicaDistribution is entered.
func (s *BaseDorisParserListener) EnterShowReplicaDistribution(ctx *ShowReplicaDistributionContext) {}

// ExitShowReplicaDistribution is called when production showReplicaDistribution is exited.
func (s *BaseDorisParserListener) ExitShowReplicaDistribution(ctx *ShowReplicaDistributionContext) {}

// EnterShowResources is called when production showResources is entered.
func (s *BaseDorisParserListener) EnterShowResources(ctx *ShowResourcesContext) {}

// ExitShowResources is called when production showResources is exited.
func (s *BaseDorisParserListener) ExitShowResources(ctx *ShowResourcesContext) {}

// EnterShowLoad is called when production showLoad is entered.
func (s *BaseDorisParserListener) EnterShowLoad(ctx *ShowLoadContext) {}

// ExitShowLoad is called when production showLoad is exited.
func (s *BaseDorisParserListener) ExitShowLoad(ctx *ShowLoadContext) {}

// EnterShowLoadWarings is called when production showLoadWarings is entered.
func (s *BaseDorisParserListener) EnterShowLoadWarings(ctx *ShowLoadWaringsContext) {}

// ExitShowLoadWarings is called when production showLoadWarings is exited.
func (s *BaseDorisParserListener) ExitShowLoadWarings(ctx *ShowLoadWaringsContext) {}

// EnterShowTriggers is called when production showTriggers is entered.
func (s *BaseDorisParserListener) EnterShowTriggers(ctx *ShowTriggersContext) {}

// ExitShowTriggers is called when production showTriggers is exited.
func (s *BaseDorisParserListener) ExitShowTriggers(ctx *ShowTriggersContext) {}

// EnterShowDiagnoseTablet is called when production showDiagnoseTablet is entered.
func (s *BaseDorisParserListener) EnterShowDiagnoseTablet(ctx *ShowDiagnoseTabletContext) {}

// ExitShowDiagnoseTablet is called when production showDiagnoseTablet is exited.
func (s *BaseDorisParserListener) ExitShowDiagnoseTablet(ctx *ShowDiagnoseTabletContext) {}

// EnterShowOpenTables is called when production showOpenTables is entered.
func (s *BaseDorisParserListener) EnterShowOpenTables(ctx *ShowOpenTablesContext) {}

// ExitShowOpenTables is called when production showOpenTables is exited.
func (s *BaseDorisParserListener) ExitShowOpenTables(ctx *ShowOpenTablesContext) {}

// EnterShowFrontends is called when production showFrontends is entered.
func (s *BaseDorisParserListener) EnterShowFrontends(ctx *ShowFrontendsContext) {}

// ExitShowFrontends is called when production showFrontends is exited.
func (s *BaseDorisParserListener) ExitShowFrontends(ctx *ShowFrontendsContext) {}

// EnterShowDatabaseId is called when production showDatabaseId is entered.
func (s *BaseDorisParserListener) EnterShowDatabaseId(ctx *ShowDatabaseIdContext) {}

// ExitShowDatabaseId is called when production showDatabaseId is exited.
func (s *BaseDorisParserListener) ExitShowDatabaseId(ctx *ShowDatabaseIdContext) {}

// EnterShowColumns is called when production showColumns is entered.
func (s *BaseDorisParserListener) EnterShowColumns(ctx *ShowColumnsContext) {}

// ExitShowColumns is called when production showColumns is exited.
func (s *BaseDorisParserListener) ExitShowColumns(ctx *ShowColumnsContext) {}

// EnterShowTableId is called when production showTableId is entered.
func (s *BaseDorisParserListener) EnterShowTableId(ctx *ShowTableIdContext) {}

// ExitShowTableId is called when production showTableId is exited.
func (s *BaseDorisParserListener) ExitShowTableId(ctx *ShowTableIdContext) {}

// EnterShowTrash is called when production showTrash is entered.
func (s *BaseDorisParserListener) EnterShowTrash(ctx *ShowTrashContext) {}

// ExitShowTrash is called when production showTrash is exited.
func (s *BaseDorisParserListener) ExitShowTrash(ctx *ShowTrashContext) {}

// EnterShowTypeCast is called when production showTypeCast is entered.
func (s *BaseDorisParserListener) EnterShowTypeCast(ctx *ShowTypeCastContext) {}

// ExitShowTypeCast is called when production showTypeCast is exited.
func (s *BaseDorisParserListener) ExitShowTypeCast(ctx *ShowTypeCastContext) {}

// EnterShowClusters is called when production showClusters is entered.
func (s *BaseDorisParserListener) EnterShowClusters(ctx *ShowClustersContext) {}

// ExitShowClusters is called when production showClusters is exited.
func (s *BaseDorisParserListener) ExitShowClusters(ctx *ShowClustersContext) {}

// EnterShowStatus is called when production showStatus is entered.
func (s *BaseDorisParserListener) EnterShowStatus(ctx *ShowStatusContext) {}

// ExitShowStatus is called when production showStatus is exited.
func (s *BaseDorisParserListener) ExitShowStatus(ctx *ShowStatusContext) {}

// EnterShowWhitelist is called when production showWhitelist is entered.
func (s *BaseDorisParserListener) EnterShowWhitelist(ctx *ShowWhitelistContext) {}

// ExitShowWhitelist is called when production showWhitelist is exited.
func (s *BaseDorisParserListener) ExitShowWhitelist(ctx *ShowWhitelistContext) {}

// EnterShowTabletsBelong is called when production showTabletsBelong is entered.
func (s *BaseDorisParserListener) EnterShowTabletsBelong(ctx *ShowTabletsBelongContext) {}

// ExitShowTabletsBelong is called when production showTabletsBelong is exited.
func (s *BaseDorisParserListener) ExitShowTabletsBelong(ctx *ShowTabletsBelongContext) {}

// EnterShowDataSkew is called when production showDataSkew is entered.
func (s *BaseDorisParserListener) EnterShowDataSkew(ctx *ShowDataSkewContext) {}

// ExitShowDataSkew is called when production showDataSkew is exited.
func (s *BaseDorisParserListener) ExitShowDataSkew(ctx *ShowDataSkewContext) {}

// EnterShowTableCreation is called when production showTableCreation is entered.
func (s *BaseDorisParserListener) EnterShowTableCreation(ctx *ShowTableCreationContext) {}

// ExitShowTableCreation is called when production showTableCreation is exited.
func (s *BaseDorisParserListener) ExitShowTableCreation(ctx *ShowTableCreationContext) {}

// EnterShowTabletStorageFormat is called when production showTabletStorageFormat is entered.
func (s *BaseDorisParserListener) EnterShowTabletStorageFormat(ctx *ShowTabletStorageFormatContext) {}

// ExitShowTabletStorageFormat is called when production showTabletStorageFormat is exited.
func (s *BaseDorisParserListener) ExitShowTabletStorageFormat(ctx *ShowTabletStorageFormatContext) {}

// EnterShowQueryProfile is called when production showQueryProfile is entered.
func (s *BaseDorisParserListener) EnterShowQueryProfile(ctx *ShowQueryProfileContext) {}

// ExitShowQueryProfile is called when production showQueryProfile is exited.
func (s *BaseDorisParserListener) ExitShowQueryProfile(ctx *ShowQueryProfileContext) {}

// EnterShowConvertLsc is called when production showConvertLsc is entered.
func (s *BaseDorisParserListener) EnterShowConvertLsc(ctx *ShowConvertLscContext) {}

// ExitShowConvertLsc is called when production showConvertLsc is exited.
func (s *BaseDorisParserListener) ExitShowConvertLsc(ctx *ShowConvertLscContext) {}

// EnterShowTables is called when production showTables is entered.
func (s *BaseDorisParserListener) EnterShowTables(ctx *ShowTablesContext) {}

// ExitShowTables is called when production showTables is exited.
func (s *BaseDorisParserListener) ExitShowTables(ctx *ShowTablesContext) {}

// EnterShowViews is called when production showViews is entered.
func (s *BaseDorisParserListener) EnterShowViews(ctx *ShowViewsContext) {}

// ExitShowViews is called when production showViews is exited.
func (s *BaseDorisParserListener) ExitShowViews(ctx *ShowViewsContext) {}

// EnterShowTableStatus is called when production showTableStatus is entered.
func (s *BaseDorisParserListener) EnterShowTableStatus(ctx *ShowTableStatusContext) {}

// ExitShowTableStatus is called when production showTableStatus is exited.
func (s *BaseDorisParserListener) ExitShowTableStatus(ctx *ShowTableStatusContext) {}

// EnterShowDatabases is called when production showDatabases is entered.
func (s *BaseDorisParserListener) EnterShowDatabases(ctx *ShowDatabasesContext) {}

// ExitShowDatabases is called when production showDatabases is exited.
func (s *BaseDorisParserListener) ExitShowDatabases(ctx *ShowDatabasesContext) {}

// EnterShowTabletsFromTable is called when production showTabletsFromTable is entered.
func (s *BaseDorisParserListener) EnterShowTabletsFromTable(ctx *ShowTabletsFromTableContext) {}

// ExitShowTabletsFromTable is called when production showTabletsFromTable is exited.
func (s *BaseDorisParserListener) ExitShowTabletsFromTable(ctx *ShowTabletsFromTableContext) {}

// EnterShowCatalogRecycleBin is called when production showCatalogRecycleBin is entered.
func (s *BaseDorisParserListener) EnterShowCatalogRecycleBin(ctx *ShowCatalogRecycleBinContext) {}

// ExitShowCatalogRecycleBin is called when production showCatalogRecycleBin is exited.
func (s *BaseDorisParserListener) ExitShowCatalogRecycleBin(ctx *ShowCatalogRecycleBinContext) {}

// EnterShowTabletId is called when production showTabletId is entered.
func (s *BaseDorisParserListener) EnterShowTabletId(ctx *ShowTabletIdContext) {}

// ExitShowTabletId is called when production showTabletId is exited.
func (s *BaseDorisParserListener) ExitShowTabletId(ctx *ShowTabletIdContext) {}

// EnterShowDictionaries is called when production showDictionaries is entered.
func (s *BaseDorisParserListener) EnterShowDictionaries(ctx *ShowDictionariesContext) {}

// ExitShowDictionaries is called when production showDictionaries is exited.
func (s *BaseDorisParserListener) ExitShowDictionaries(ctx *ShowDictionariesContext) {}

// EnterShowTransaction is called when production showTransaction is entered.
func (s *BaseDorisParserListener) EnterShowTransaction(ctx *ShowTransactionContext) {}

// ExitShowTransaction is called when production showTransaction is exited.
func (s *BaseDorisParserListener) ExitShowTransaction(ctx *ShowTransactionContext) {}

// EnterShowReplicaStatus is called when production showReplicaStatus is entered.
func (s *BaseDorisParserListener) EnterShowReplicaStatus(ctx *ShowReplicaStatusContext) {}

// ExitShowReplicaStatus is called when production showReplicaStatus is exited.
func (s *BaseDorisParserListener) ExitShowReplicaStatus(ctx *ShowReplicaStatusContext) {}

// EnterShowWorkloadGroups is called when production showWorkloadGroups is entered.
func (s *BaseDorisParserListener) EnterShowWorkloadGroups(ctx *ShowWorkloadGroupsContext) {}

// ExitShowWorkloadGroups is called when production showWorkloadGroups is exited.
func (s *BaseDorisParserListener) ExitShowWorkloadGroups(ctx *ShowWorkloadGroupsContext) {}

// EnterShowCopy is called when production showCopy is entered.
func (s *BaseDorisParserListener) EnterShowCopy(ctx *ShowCopyContext) {}

// ExitShowCopy is called when production showCopy is exited.
func (s *BaseDorisParserListener) ExitShowCopy(ctx *ShowCopyContext) {}

// EnterShowQueryStats is called when production showQueryStats is entered.
func (s *BaseDorisParserListener) EnterShowQueryStats(ctx *ShowQueryStatsContext) {}

// ExitShowQueryStats is called when production showQueryStats is exited.
func (s *BaseDorisParserListener) ExitShowQueryStats(ctx *ShowQueryStatsContext) {}

// EnterShowIndex is called when production showIndex is entered.
func (s *BaseDorisParserListener) EnterShowIndex(ctx *ShowIndexContext) {}

// ExitShowIndex is called when production showIndex is exited.
func (s *BaseDorisParserListener) ExitShowIndex(ctx *ShowIndexContext) {}

// EnterShowWarmUpJob is called when production showWarmUpJob is entered.
func (s *BaseDorisParserListener) EnterShowWarmUpJob(ctx *ShowWarmUpJobContext) {}

// ExitShowWarmUpJob is called when production showWarmUpJob is exited.
func (s *BaseDorisParserListener) ExitShowWarmUpJob(ctx *ShowWarmUpJobContext) {}

// EnterSync is called when production sync is entered.
func (s *BaseDorisParserListener) EnterSync(ctx *SyncContext) {}

// ExitSync is called when production sync is exited.
func (s *BaseDorisParserListener) ExitSync(ctx *SyncContext) {}

// EnterCreateRoutineLoadAlias is called when production createRoutineLoadAlias is entered.
func (s *BaseDorisParserListener) EnterCreateRoutineLoadAlias(ctx *CreateRoutineLoadAliasContext) {}

// ExitCreateRoutineLoadAlias is called when production createRoutineLoadAlias is exited.
func (s *BaseDorisParserListener) ExitCreateRoutineLoadAlias(ctx *CreateRoutineLoadAliasContext) {}

// EnterShowCreateRoutineLoad is called when production showCreateRoutineLoad is entered.
func (s *BaseDorisParserListener) EnterShowCreateRoutineLoad(ctx *ShowCreateRoutineLoadContext) {}

// ExitShowCreateRoutineLoad is called when production showCreateRoutineLoad is exited.
func (s *BaseDorisParserListener) ExitShowCreateRoutineLoad(ctx *ShowCreateRoutineLoadContext) {}

// EnterPauseRoutineLoad is called when production pauseRoutineLoad is entered.
func (s *BaseDorisParserListener) EnterPauseRoutineLoad(ctx *PauseRoutineLoadContext) {}

// ExitPauseRoutineLoad is called when production pauseRoutineLoad is exited.
func (s *BaseDorisParserListener) ExitPauseRoutineLoad(ctx *PauseRoutineLoadContext) {}

// EnterPauseAllRoutineLoad is called when production pauseAllRoutineLoad is entered.
func (s *BaseDorisParserListener) EnterPauseAllRoutineLoad(ctx *PauseAllRoutineLoadContext) {}

// ExitPauseAllRoutineLoad is called when production pauseAllRoutineLoad is exited.
func (s *BaseDorisParserListener) ExitPauseAllRoutineLoad(ctx *PauseAllRoutineLoadContext) {}

// EnterResumeRoutineLoad is called when production resumeRoutineLoad is entered.
func (s *BaseDorisParserListener) EnterResumeRoutineLoad(ctx *ResumeRoutineLoadContext) {}

// ExitResumeRoutineLoad is called when production resumeRoutineLoad is exited.
func (s *BaseDorisParserListener) ExitResumeRoutineLoad(ctx *ResumeRoutineLoadContext) {}

// EnterResumeAllRoutineLoad is called when production resumeAllRoutineLoad is entered.
func (s *BaseDorisParserListener) EnterResumeAllRoutineLoad(ctx *ResumeAllRoutineLoadContext) {}

// ExitResumeAllRoutineLoad is called when production resumeAllRoutineLoad is exited.
func (s *BaseDorisParserListener) ExitResumeAllRoutineLoad(ctx *ResumeAllRoutineLoadContext) {}

// EnterStopRoutineLoad is called when production stopRoutineLoad is entered.
func (s *BaseDorisParserListener) EnterStopRoutineLoad(ctx *StopRoutineLoadContext) {}

// ExitStopRoutineLoad is called when production stopRoutineLoad is exited.
func (s *BaseDorisParserListener) ExitStopRoutineLoad(ctx *StopRoutineLoadContext) {}

// EnterShowRoutineLoad is called when production showRoutineLoad is entered.
func (s *BaseDorisParserListener) EnterShowRoutineLoad(ctx *ShowRoutineLoadContext) {}

// ExitShowRoutineLoad is called when production showRoutineLoad is exited.
func (s *BaseDorisParserListener) ExitShowRoutineLoad(ctx *ShowRoutineLoadContext) {}

// EnterShowRoutineLoadTask is called when production showRoutineLoadTask is entered.
func (s *BaseDorisParserListener) EnterShowRoutineLoadTask(ctx *ShowRoutineLoadTaskContext) {}

// ExitShowRoutineLoadTask is called when production showRoutineLoadTask is exited.
func (s *BaseDorisParserListener) ExitShowRoutineLoadTask(ctx *ShowRoutineLoadTaskContext) {}

// EnterShowIndexAnalyzer is called when production showIndexAnalyzer is entered.
func (s *BaseDorisParserListener) EnterShowIndexAnalyzer(ctx *ShowIndexAnalyzerContext) {}

// ExitShowIndexAnalyzer is called when production showIndexAnalyzer is exited.
func (s *BaseDorisParserListener) ExitShowIndexAnalyzer(ctx *ShowIndexAnalyzerContext) {}

// EnterShowIndexTokenizer is called when production showIndexTokenizer is entered.
func (s *BaseDorisParserListener) EnterShowIndexTokenizer(ctx *ShowIndexTokenizerContext) {}

// ExitShowIndexTokenizer is called when production showIndexTokenizer is exited.
func (s *BaseDorisParserListener) ExitShowIndexTokenizer(ctx *ShowIndexTokenizerContext) {}

// EnterShowIndexTokenFilter is called when production showIndexTokenFilter is entered.
func (s *BaseDorisParserListener) EnterShowIndexTokenFilter(ctx *ShowIndexTokenFilterContext) {}

// ExitShowIndexTokenFilter is called when production showIndexTokenFilter is exited.
func (s *BaseDorisParserListener) ExitShowIndexTokenFilter(ctx *ShowIndexTokenFilterContext) {}

// EnterKillConnection is called when production killConnection is entered.
func (s *BaseDorisParserListener) EnterKillConnection(ctx *KillConnectionContext) {}

// ExitKillConnection is called when production killConnection is exited.
func (s *BaseDorisParserListener) ExitKillConnection(ctx *KillConnectionContext) {}

// EnterKillQuery is called when production killQuery is entered.
func (s *BaseDorisParserListener) EnterKillQuery(ctx *KillQueryContext) {}

// ExitKillQuery is called when production killQuery is exited.
func (s *BaseDorisParserListener) ExitKillQuery(ctx *KillQueryContext) {}

// EnterHelp is called when production help is entered.
func (s *BaseDorisParserListener) EnterHelp(ctx *HelpContext) {}

// ExitHelp is called when production help is exited.
func (s *BaseDorisParserListener) ExitHelp(ctx *HelpContext) {}

// EnterUnlockTables is called when production unlockTables is entered.
func (s *BaseDorisParserListener) EnterUnlockTables(ctx *UnlockTablesContext) {}

// ExitUnlockTables is called when production unlockTables is exited.
func (s *BaseDorisParserListener) ExitUnlockTables(ctx *UnlockTablesContext) {}

// EnterInstallPlugin is called when production installPlugin is entered.
func (s *BaseDorisParserListener) EnterInstallPlugin(ctx *InstallPluginContext) {}

// ExitInstallPlugin is called when production installPlugin is exited.
func (s *BaseDorisParserListener) ExitInstallPlugin(ctx *InstallPluginContext) {}

// EnterUninstallPlugin is called when production uninstallPlugin is entered.
func (s *BaseDorisParserListener) EnterUninstallPlugin(ctx *UninstallPluginContext) {}

// ExitUninstallPlugin is called when production uninstallPlugin is exited.
func (s *BaseDorisParserListener) ExitUninstallPlugin(ctx *UninstallPluginContext) {}

// EnterLockTables is called when production lockTables is entered.
func (s *BaseDorisParserListener) EnterLockTables(ctx *LockTablesContext) {}

// ExitLockTables is called when production lockTables is exited.
func (s *BaseDorisParserListener) ExitLockTables(ctx *LockTablesContext) {}

// EnterRestore is called when production restore is entered.
func (s *BaseDorisParserListener) EnterRestore(ctx *RestoreContext) {}

// ExitRestore is called when production restore is exited.
func (s *BaseDorisParserListener) ExitRestore(ctx *RestoreContext) {}

// EnterWarmUpCluster is called when production warmUpCluster is entered.
func (s *BaseDorisParserListener) EnterWarmUpCluster(ctx *WarmUpClusterContext) {}

// ExitWarmUpCluster is called when production warmUpCluster is exited.
func (s *BaseDorisParserListener) ExitWarmUpCluster(ctx *WarmUpClusterContext) {}

// EnterBackup is called when production backup is entered.
func (s *BaseDorisParserListener) EnterBackup(ctx *BackupContext) {}

// ExitBackup is called when production backup is exited.
func (s *BaseDorisParserListener) ExitBackup(ctx *BackupContext) {}

// EnterUnsupportedStartTransaction is called when production unsupportedStartTransaction is entered.
func (s *BaseDorisParserListener) EnterUnsupportedStartTransaction(ctx *UnsupportedStartTransactionContext) {
}

// ExitUnsupportedStartTransaction is called when production unsupportedStartTransaction is exited.
func (s *BaseDorisParserListener) ExitUnsupportedStartTransaction(ctx *UnsupportedStartTransactionContext) {
}

// EnterWarmUpItem is called when production warmUpItem is entered.
func (s *BaseDorisParserListener) EnterWarmUpItem(ctx *WarmUpItemContext) {}

// ExitWarmUpItem is called when production warmUpItem is exited.
func (s *BaseDorisParserListener) ExitWarmUpItem(ctx *WarmUpItemContext) {}

// EnterLockTable is called when production lockTable is entered.
func (s *BaseDorisParserListener) EnterLockTable(ctx *LockTableContext) {}

// ExitLockTable is called when production lockTable is exited.
func (s *BaseDorisParserListener) ExitLockTable(ctx *LockTableContext) {}

// EnterCreateRoutineLoad is called when production createRoutineLoad is entered.
func (s *BaseDorisParserListener) EnterCreateRoutineLoad(ctx *CreateRoutineLoadContext) {}

// ExitCreateRoutineLoad is called when production createRoutineLoad is exited.
func (s *BaseDorisParserListener) ExitCreateRoutineLoad(ctx *CreateRoutineLoadContext) {}

// EnterMysqlLoad is called when production mysqlLoad is entered.
func (s *BaseDorisParserListener) EnterMysqlLoad(ctx *MysqlLoadContext) {}

// ExitMysqlLoad is called when production mysqlLoad is exited.
func (s *BaseDorisParserListener) ExitMysqlLoad(ctx *MysqlLoadContext) {}

// EnterShowCreateLoad is called when production showCreateLoad is entered.
func (s *BaseDorisParserListener) EnterShowCreateLoad(ctx *ShowCreateLoadContext) {}

// ExitShowCreateLoad is called when production showCreateLoad is exited.
func (s *BaseDorisParserListener) ExitShowCreateLoad(ctx *ShowCreateLoadContext) {}

// EnterSeparator is called when production separator is entered.
func (s *BaseDorisParserListener) EnterSeparator(ctx *SeparatorContext) {}

// ExitSeparator is called when production separator is exited.
func (s *BaseDorisParserListener) ExitSeparator(ctx *SeparatorContext) {}

// EnterImportColumns is called when production importColumns is entered.
func (s *BaseDorisParserListener) EnterImportColumns(ctx *ImportColumnsContext) {}

// ExitImportColumns is called when production importColumns is exited.
func (s *BaseDorisParserListener) ExitImportColumns(ctx *ImportColumnsContext) {}

// EnterImportPrecedingFilter is called when production importPrecedingFilter is entered.
func (s *BaseDorisParserListener) EnterImportPrecedingFilter(ctx *ImportPrecedingFilterContext) {}

// ExitImportPrecedingFilter is called when production importPrecedingFilter is exited.
func (s *BaseDorisParserListener) ExitImportPrecedingFilter(ctx *ImportPrecedingFilterContext) {}

// EnterImportWhere is called when production importWhere is entered.
func (s *BaseDorisParserListener) EnterImportWhere(ctx *ImportWhereContext) {}

// ExitImportWhere is called when production importWhere is exited.
func (s *BaseDorisParserListener) ExitImportWhere(ctx *ImportWhereContext) {}

// EnterImportDeleteOn is called when production importDeleteOn is entered.
func (s *BaseDorisParserListener) EnterImportDeleteOn(ctx *ImportDeleteOnContext) {}

// ExitImportDeleteOn is called when production importDeleteOn is exited.
func (s *BaseDorisParserListener) ExitImportDeleteOn(ctx *ImportDeleteOnContext) {}

// EnterImportSequence is called when production importSequence is entered.
func (s *BaseDorisParserListener) EnterImportSequence(ctx *ImportSequenceContext) {}

// ExitImportSequence is called when production importSequence is exited.
func (s *BaseDorisParserListener) ExitImportSequence(ctx *ImportSequenceContext) {}

// EnterImportPartitions is called when production importPartitions is entered.
func (s *BaseDorisParserListener) EnterImportPartitions(ctx *ImportPartitionsContext) {}

// ExitImportPartitions is called when production importPartitions is exited.
func (s *BaseDorisParserListener) ExitImportPartitions(ctx *ImportPartitionsContext) {}

// EnterImportSequenceStatement is called when production importSequenceStatement is entered.
func (s *BaseDorisParserListener) EnterImportSequenceStatement(ctx *ImportSequenceStatementContext) {}

// ExitImportSequenceStatement is called when production importSequenceStatement is exited.
func (s *BaseDorisParserListener) ExitImportSequenceStatement(ctx *ImportSequenceStatementContext) {}

// EnterImportDeleteOnStatement is called when production importDeleteOnStatement is entered.
func (s *BaseDorisParserListener) EnterImportDeleteOnStatement(ctx *ImportDeleteOnStatementContext) {}

// ExitImportDeleteOnStatement is called when production importDeleteOnStatement is exited.
func (s *BaseDorisParserListener) ExitImportDeleteOnStatement(ctx *ImportDeleteOnStatementContext) {}

// EnterImportWhereStatement is called when production importWhereStatement is entered.
func (s *BaseDorisParserListener) EnterImportWhereStatement(ctx *ImportWhereStatementContext) {}

// ExitImportWhereStatement is called when production importWhereStatement is exited.
func (s *BaseDorisParserListener) ExitImportWhereStatement(ctx *ImportWhereStatementContext) {}

// EnterImportPrecedingFilterStatement is called when production importPrecedingFilterStatement is entered.
func (s *BaseDorisParserListener) EnterImportPrecedingFilterStatement(ctx *ImportPrecedingFilterStatementContext) {
}

// ExitImportPrecedingFilterStatement is called when production importPrecedingFilterStatement is exited.
func (s *BaseDorisParserListener) ExitImportPrecedingFilterStatement(ctx *ImportPrecedingFilterStatementContext) {
}

// EnterImportColumnsStatement is called when production importColumnsStatement is entered.
func (s *BaseDorisParserListener) EnterImportColumnsStatement(ctx *ImportColumnsStatementContext) {}

// ExitImportColumnsStatement is called when production importColumnsStatement is exited.
func (s *BaseDorisParserListener) ExitImportColumnsStatement(ctx *ImportColumnsStatementContext) {}

// EnterImportColumnDesc is called when production importColumnDesc is entered.
func (s *BaseDorisParserListener) EnterImportColumnDesc(ctx *ImportColumnDescContext) {}

// ExitImportColumnDesc is called when production importColumnDesc is exited.
func (s *BaseDorisParserListener) ExitImportColumnDesc(ctx *ImportColumnDescContext) {}

// EnterRefreshCatalog is called when production refreshCatalog is entered.
func (s *BaseDorisParserListener) EnterRefreshCatalog(ctx *RefreshCatalogContext) {}

// ExitRefreshCatalog is called when production refreshCatalog is exited.
func (s *BaseDorisParserListener) ExitRefreshCatalog(ctx *RefreshCatalogContext) {}

// EnterRefreshDatabase is called when production refreshDatabase is entered.
func (s *BaseDorisParserListener) EnterRefreshDatabase(ctx *RefreshDatabaseContext) {}

// ExitRefreshDatabase is called when production refreshDatabase is exited.
func (s *BaseDorisParserListener) ExitRefreshDatabase(ctx *RefreshDatabaseContext) {}

// EnterRefreshTable is called when production refreshTable is entered.
func (s *BaseDorisParserListener) EnterRefreshTable(ctx *RefreshTableContext) {}

// ExitRefreshTable is called when production refreshTable is exited.
func (s *BaseDorisParserListener) ExitRefreshTable(ctx *RefreshTableContext) {}

// EnterRefreshDictionary is called when production refreshDictionary is entered.
func (s *BaseDorisParserListener) EnterRefreshDictionary(ctx *RefreshDictionaryContext) {}

// ExitRefreshDictionary is called when production refreshDictionary is exited.
func (s *BaseDorisParserListener) ExitRefreshDictionary(ctx *RefreshDictionaryContext) {}

// EnterRefreshLdap is called when production refreshLdap is entered.
func (s *BaseDorisParserListener) EnterRefreshLdap(ctx *RefreshLdapContext) {}

// ExitRefreshLdap is called when production refreshLdap is exited.
func (s *BaseDorisParserListener) ExitRefreshLdap(ctx *RefreshLdapContext) {}

// EnterCleanAllProfile is called when production cleanAllProfile is entered.
func (s *BaseDorisParserListener) EnterCleanAllProfile(ctx *CleanAllProfileContext) {}

// ExitCleanAllProfile is called when production cleanAllProfile is exited.
func (s *BaseDorisParserListener) ExitCleanAllProfile(ctx *CleanAllProfileContext) {}

// EnterCleanLabel is called when production cleanLabel is entered.
func (s *BaseDorisParserListener) EnterCleanLabel(ctx *CleanLabelContext) {}

// ExitCleanLabel is called when production cleanLabel is exited.
func (s *BaseDorisParserListener) ExitCleanLabel(ctx *CleanLabelContext) {}

// EnterCleanQueryStats is called when production cleanQueryStats is entered.
func (s *BaseDorisParserListener) EnterCleanQueryStats(ctx *CleanQueryStatsContext) {}

// ExitCleanQueryStats is called when production cleanQueryStats is exited.
func (s *BaseDorisParserListener) ExitCleanQueryStats(ctx *CleanQueryStatsContext) {}

// EnterCleanAllQueryStats is called when production cleanAllQueryStats is entered.
func (s *BaseDorisParserListener) EnterCleanAllQueryStats(ctx *CleanAllQueryStatsContext) {}

// ExitCleanAllQueryStats is called when production cleanAllQueryStats is exited.
func (s *BaseDorisParserListener) ExitCleanAllQueryStats(ctx *CleanAllQueryStatsContext) {}

// EnterCancelLoad is called when production cancelLoad is entered.
func (s *BaseDorisParserListener) EnterCancelLoad(ctx *CancelLoadContext) {}

// ExitCancelLoad is called when production cancelLoad is exited.
func (s *BaseDorisParserListener) ExitCancelLoad(ctx *CancelLoadContext) {}

// EnterCancelExport is called when production cancelExport is entered.
func (s *BaseDorisParserListener) EnterCancelExport(ctx *CancelExportContext) {}

// ExitCancelExport is called when production cancelExport is exited.
func (s *BaseDorisParserListener) ExitCancelExport(ctx *CancelExportContext) {}

// EnterCancelWarmUpJob is called when production cancelWarmUpJob is entered.
func (s *BaseDorisParserListener) EnterCancelWarmUpJob(ctx *CancelWarmUpJobContext) {}

// ExitCancelWarmUpJob is called when production cancelWarmUpJob is exited.
func (s *BaseDorisParserListener) ExitCancelWarmUpJob(ctx *CancelWarmUpJobContext) {}

// EnterCancelDecommisionBackend is called when production cancelDecommisionBackend is entered.
func (s *BaseDorisParserListener) EnterCancelDecommisionBackend(ctx *CancelDecommisionBackendContext) {
}

// ExitCancelDecommisionBackend is called when production cancelDecommisionBackend is exited.
func (s *BaseDorisParserListener) ExitCancelDecommisionBackend(ctx *CancelDecommisionBackendContext) {
}

// EnterCancelBackup is called when production cancelBackup is entered.
func (s *BaseDorisParserListener) EnterCancelBackup(ctx *CancelBackupContext) {}

// ExitCancelBackup is called when production cancelBackup is exited.
func (s *BaseDorisParserListener) ExitCancelBackup(ctx *CancelBackupContext) {}

// EnterCancelRestore is called when production cancelRestore is entered.
func (s *BaseDorisParserListener) EnterCancelRestore(ctx *CancelRestoreContext) {}

// ExitCancelRestore is called when production cancelRestore is exited.
func (s *BaseDorisParserListener) ExitCancelRestore(ctx *CancelRestoreContext) {}

// EnterCancelBuildIndex is called when production cancelBuildIndex is entered.
func (s *BaseDorisParserListener) EnterCancelBuildIndex(ctx *CancelBuildIndexContext) {}

// ExitCancelBuildIndex is called when production cancelBuildIndex is exited.
func (s *BaseDorisParserListener) ExitCancelBuildIndex(ctx *CancelBuildIndexContext) {}

// EnterCancelAlterTable is called when production cancelAlterTable is entered.
func (s *BaseDorisParserListener) EnterCancelAlterTable(ctx *CancelAlterTableContext) {}

// ExitCancelAlterTable is called when production cancelAlterTable is exited.
func (s *BaseDorisParserListener) ExitCancelAlterTable(ctx *CancelAlterTableContext) {}

// EnterAdminShowReplicaDistribution is called when production adminShowReplicaDistribution is entered.
func (s *BaseDorisParserListener) EnterAdminShowReplicaDistribution(ctx *AdminShowReplicaDistributionContext) {
}

// ExitAdminShowReplicaDistribution is called when production adminShowReplicaDistribution is exited.
func (s *BaseDorisParserListener) ExitAdminShowReplicaDistribution(ctx *AdminShowReplicaDistributionContext) {
}

// EnterAdminRebalanceDisk is called when production adminRebalanceDisk is entered.
func (s *BaseDorisParserListener) EnterAdminRebalanceDisk(ctx *AdminRebalanceDiskContext) {}

// ExitAdminRebalanceDisk is called when production adminRebalanceDisk is exited.
func (s *BaseDorisParserListener) ExitAdminRebalanceDisk(ctx *AdminRebalanceDiskContext) {}

// EnterAdminCancelRebalanceDisk is called when production adminCancelRebalanceDisk is entered.
func (s *BaseDorisParserListener) EnterAdminCancelRebalanceDisk(ctx *AdminCancelRebalanceDiskContext) {
}

// ExitAdminCancelRebalanceDisk is called when production adminCancelRebalanceDisk is exited.
func (s *BaseDorisParserListener) ExitAdminCancelRebalanceDisk(ctx *AdminCancelRebalanceDiskContext) {
}

// EnterAdminDiagnoseTablet is called when production adminDiagnoseTablet is entered.
func (s *BaseDorisParserListener) EnterAdminDiagnoseTablet(ctx *AdminDiagnoseTabletContext) {}

// ExitAdminDiagnoseTablet is called when production adminDiagnoseTablet is exited.
func (s *BaseDorisParserListener) ExitAdminDiagnoseTablet(ctx *AdminDiagnoseTabletContext) {}

// EnterAdminShowReplicaStatus is called when production adminShowReplicaStatus is entered.
func (s *BaseDorisParserListener) EnterAdminShowReplicaStatus(ctx *AdminShowReplicaStatusContext) {}

// ExitAdminShowReplicaStatus is called when production adminShowReplicaStatus is exited.
func (s *BaseDorisParserListener) ExitAdminShowReplicaStatus(ctx *AdminShowReplicaStatusContext) {}

// EnterAdminCompactTable is called when production adminCompactTable is entered.
func (s *BaseDorisParserListener) EnterAdminCompactTable(ctx *AdminCompactTableContext) {}

// ExitAdminCompactTable is called when production adminCompactTable is exited.
func (s *BaseDorisParserListener) ExitAdminCompactTable(ctx *AdminCompactTableContext) {}

// EnterAdminCheckTablets is called when production adminCheckTablets is entered.
func (s *BaseDorisParserListener) EnterAdminCheckTablets(ctx *AdminCheckTabletsContext) {}

// ExitAdminCheckTablets is called when production adminCheckTablets is exited.
func (s *BaseDorisParserListener) ExitAdminCheckTablets(ctx *AdminCheckTabletsContext) {}

// EnterAdminShowTabletStorageFormat is called when production adminShowTabletStorageFormat is entered.
func (s *BaseDorisParserListener) EnterAdminShowTabletStorageFormat(ctx *AdminShowTabletStorageFormatContext) {
}

// ExitAdminShowTabletStorageFormat is called when production adminShowTabletStorageFormat is exited.
func (s *BaseDorisParserListener) ExitAdminShowTabletStorageFormat(ctx *AdminShowTabletStorageFormatContext) {
}

// EnterAdminSetFrontendConfig is called when production adminSetFrontendConfig is entered.
func (s *BaseDorisParserListener) EnterAdminSetFrontendConfig(ctx *AdminSetFrontendConfigContext) {}

// ExitAdminSetFrontendConfig is called when production adminSetFrontendConfig is exited.
func (s *BaseDorisParserListener) ExitAdminSetFrontendConfig(ctx *AdminSetFrontendConfigContext) {}

// EnterAdminCleanTrash is called when production adminCleanTrash is entered.
func (s *BaseDorisParserListener) EnterAdminCleanTrash(ctx *AdminCleanTrashContext) {}

// ExitAdminCleanTrash is called when production adminCleanTrash is exited.
func (s *BaseDorisParserListener) ExitAdminCleanTrash(ctx *AdminCleanTrashContext) {}

// EnterAdminSetReplicaVersion is called when production adminSetReplicaVersion is entered.
func (s *BaseDorisParserListener) EnterAdminSetReplicaVersion(ctx *AdminSetReplicaVersionContext) {}

// ExitAdminSetReplicaVersion is called when production adminSetReplicaVersion is exited.
func (s *BaseDorisParserListener) ExitAdminSetReplicaVersion(ctx *AdminSetReplicaVersionContext) {}

// EnterAdminSetTableStatus is called when production adminSetTableStatus is entered.
func (s *BaseDorisParserListener) EnterAdminSetTableStatus(ctx *AdminSetTableStatusContext) {}

// ExitAdminSetTableStatus is called when production adminSetTableStatus is exited.
func (s *BaseDorisParserListener) ExitAdminSetTableStatus(ctx *AdminSetTableStatusContext) {}

// EnterAdminSetReplicaStatus is called when production adminSetReplicaStatus is entered.
func (s *BaseDorisParserListener) EnterAdminSetReplicaStatus(ctx *AdminSetReplicaStatusContext) {}

// ExitAdminSetReplicaStatus is called when production adminSetReplicaStatus is exited.
func (s *BaseDorisParserListener) ExitAdminSetReplicaStatus(ctx *AdminSetReplicaStatusContext) {}

// EnterAdminRepairTable is called when production adminRepairTable is entered.
func (s *BaseDorisParserListener) EnterAdminRepairTable(ctx *AdminRepairTableContext) {}

// ExitAdminRepairTable is called when production adminRepairTable is exited.
func (s *BaseDorisParserListener) ExitAdminRepairTable(ctx *AdminRepairTableContext) {}

// EnterAdminCancelRepairTable is called when production adminCancelRepairTable is entered.
func (s *BaseDorisParserListener) EnterAdminCancelRepairTable(ctx *AdminCancelRepairTableContext) {}

// ExitAdminCancelRepairTable is called when production adminCancelRepairTable is exited.
func (s *BaseDorisParserListener) ExitAdminCancelRepairTable(ctx *AdminCancelRepairTableContext) {}

// EnterAdminCopyTablet is called when production adminCopyTablet is entered.
func (s *BaseDorisParserListener) EnterAdminCopyTablet(ctx *AdminCopyTabletContext) {}

// ExitAdminCopyTablet is called when production adminCopyTablet is exited.
func (s *BaseDorisParserListener) ExitAdminCopyTablet(ctx *AdminCopyTabletContext) {}

// EnterRecoverDatabase is called when production recoverDatabase is entered.
func (s *BaseDorisParserListener) EnterRecoverDatabase(ctx *RecoverDatabaseContext) {}

// ExitRecoverDatabase is called when production recoverDatabase is exited.
func (s *BaseDorisParserListener) ExitRecoverDatabase(ctx *RecoverDatabaseContext) {}

// EnterRecoverTable is called when production recoverTable is entered.
func (s *BaseDorisParserListener) EnterRecoverTable(ctx *RecoverTableContext) {}

// ExitRecoverTable is called when production recoverTable is exited.
func (s *BaseDorisParserListener) ExitRecoverTable(ctx *RecoverTableContext) {}

// EnterRecoverPartition is called when production recoverPartition is entered.
func (s *BaseDorisParserListener) EnterRecoverPartition(ctx *RecoverPartitionContext) {}

// ExitRecoverPartition is called when production recoverPartition is exited.
func (s *BaseDorisParserListener) ExitRecoverPartition(ctx *RecoverPartitionContext) {}

// EnterAdminSetPartitionVersion is called when production adminSetPartitionVersion is entered.
func (s *BaseDorisParserListener) EnterAdminSetPartitionVersion(ctx *AdminSetPartitionVersionContext) {
}

// ExitAdminSetPartitionVersion is called when production adminSetPartitionVersion is exited.
func (s *BaseDorisParserListener) ExitAdminSetPartitionVersion(ctx *AdminSetPartitionVersionContext) {
}

// EnterBaseTableRef is called when production baseTableRef is entered.
func (s *BaseDorisParserListener) EnterBaseTableRef(ctx *BaseTableRefContext) {}

// ExitBaseTableRef is called when production baseTableRef is exited.
func (s *BaseDorisParserListener) ExitBaseTableRef(ctx *BaseTableRefContext) {}

// EnterWildWhere is called when production wildWhere is entered.
func (s *BaseDorisParserListener) EnterWildWhere(ctx *WildWhereContext) {}

// ExitWildWhere is called when production wildWhere is exited.
func (s *BaseDorisParserListener) ExitWildWhere(ctx *WildWhereContext) {}

// EnterTransactionBegin is called when production transactionBegin is entered.
func (s *BaseDorisParserListener) EnterTransactionBegin(ctx *TransactionBeginContext) {}

// ExitTransactionBegin is called when production transactionBegin is exited.
func (s *BaseDorisParserListener) ExitTransactionBegin(ctx *TransactionBeginContext) {}

// EnterTranscationCommit is called when production transcationCommit is entered.
func (s *BaseDorisParserListener) EnterTranscationCommit(ctx *TranscationCommitContext) {}

// ExitTranscationCommit is called when production transcationCommit is exited.
func (s *BaseDorisParserListener) ExitTranscationCommit(ctx *TranscationCommitContext) {}

// EnterTransactionRollback is called when production transactionRollback is entered.
func (s *BaseDorisParserListener) EnterTransactionRollback(ctx *TransactionRollbackContext) {}

// ExitTransactionRollback is called when production transactionRollback is exited.
func (s *BaseDorisParserListener) ExitTransactionRollback(ctx *TransactionRollbackContext) {}

// EnterGrantTablePrivilege is called when production grantTablePrivilege is entered.
func (s *BaseDorisParserListener) EnterGrantTablePrivilege(ctx *GrantTablePrivilegeContext) {}

// ExitGrantTablePrivilege is called when production grantTablePrivilege is exited.
func (s *BaseDorisParserListener) ExitGrantTablePrivilege(ctx *GrantTablePrivilegeContext) {}

// EnterGrantResourcePrivilege is called when production grantResourcePrivilege is entered.
func (s *BaseDorisParserListener) EnterGrantResourcePrivilege(ctx *GrantResourcePrivilegeContext) {}

// ExitGrantResourcePrivilege is called when production grantResourcePrivilege is exited.
func (s *BaseDorisParserListener) ExitGrantResourcePrivilege(ctx *GrantResourcePrivilegeContext) {}

// EnterGrantRole is called when production grantRole is entered.
func (s *BaseDorisParserListener) EnterGrantRole(ctx *GrantRoleContext) {}

// ExitGrantRole is called when production grantRole is exited.
func (s *BaseDorisParserListener) ExitGrantRole(ctx *GrantRoleContext) {}

// EnterRevokeRole is called when production revokeRole is entered.
func (s *BaseDorisParserListener) EnterRevokeRole(ctx *RevokeRoleContext) {}

// ExitRevokeRole is called when production revokeRole is exited.
func (s *BaseDorisParserListener) ExitRevokeRole(ctx *RevokeRoleContext) {}

// EnterRevokeResourcePrivilege is called when production revokeResourcePrivilege is entered.
func (s *BaseDorisParserListener) EnterRevokeResourcePrivilege(ctx *RevokeResourcePrivilegeContext) {}

// ExitRevokeResourcePrivilege is called when production revokeResourcePrivilege is exited.
func (s *BaseDorisParserListener) ExitRevokeResourcePrivilege(ctx *RevokeResourcePrivilegeContext) {}

// EnterRevokeTablePrivilege is called when production revokeTablePrivilege is entered.
func (s *BaseDorisParserListener) EnterRevokeTablePrivilege(ctx *RevokeTablePrivilegeContext) {}

// ExitRevokeTablePrivilege is called when production revokeTablePrivilege is exited.
func (s *BaseDorisParserListener) ExitRevokeTablePrivilege(ctx *RevokeTablePrivilegeContext) {}

// EnterPrivilege is called when production privilege is entered.
func (s *BaseDorisParserListener) EnterPrivilege(ctx *PrivilegeContext) {}

// ExitPrivilege is called when production privilege is exited.
func (s *BaseDorisParserListener) ExitPrivilege(ctx *PrivilegeContext) {}

// EnterPrivilegeList is called when production privilegeList is entered.
func (s *BaseDorisParserListener) EnterPrivilegeList(ctx *PrivilegeListContext) {}

// ExitPrivilegeList is called when production privilegeList is exited.
func (s *BaseDorisParserListener) ExitPrivilegeList(ctx *PrivilegeListContext) {}

// EnterAddBackendClause is called when production addBackendClause is entered.
func (s *BaseDorisParserListener) EnterAddBackendClause(ctx *AddBackendClauseContext) {}

// ExitAddBackendClause is called when production addBackendClause is exited.
func (s *BaseDorisParserListener) ExitAddBackendClause(ctx *AddBackendClauseContext) {}

// EnterDropBackendClause is called when production dropBackendClause is entered.
func (s *BaseDorisParserListener) EnterDropBackendClause(ctx *DropBackendClauseContext) {}

// ExitDropBackendClause is called when production dropBackendClause is exited.
func (s *BaseDorisParserListener) ExitDropBackendClause(ctx *DropBackendClauseContext) {}

// EnterDecommissionBackendClause is called when production decommissionBackendClause is entered.
func (s *BaseDorisParserListener) EnterDecommissionBackendClause(ctx *DecommissionBackendClauseContext) {
}

// ExitDecommissionBackendClause is called when production decommissionBackendClause is exited.
func (s *BaseDorisParserListener) ExitDecommissionBackendClause(ctx *DecommissionBackendClauseContext) {
}

// EnterAddObserverClause is called when production addObserverClause is entered.
func (s *BaseDorisParserListener) EnterAddObserverClause(ctx *AddObserverClauseContext) {}

// ExitAddObserverClause is called when production addObserverClause is exited.
func (s *BaseDorisParserListener) ExitAddObserverClause(ctx *AddObserverClauseContext) {}

// EnterDropObserverClause is called when production dropObserverClause is entered.
func (s *BaseDorisParserListener) EnterDropObserverClause(ctx *DropObserverClauseContext) {}

// ExitDropObserverClause is called when production dropObserverClause is exited.
func (s *BaseDorisParserListener) ExitDropObserverClause(ctx *DropObserverClauseContext) {}

// EnterAddFollowerClause is called when production addFollowerClause is entered.
func (s *BaseDorisParserListener) EnterAddFollowerClause(ctx *AddFollowerClauseContext) {}

// ExitAddFollowerClause is called when production addFollowerClause is exited.
func (s *BaseDorisParserListener) ExitAddFollowerClause(ctx *AddFollowerClauseContext) {}

// EnterDropFollowerClause is called when production dropFollowerClause is entered.
func (s *BaseDorisParserListener) EnterDropFollowerClause(ctx *DropFollowerClauseContext) {}

// ExitDropFollowerClause is called when production dropFollowerClause is exited.
func (s *BaseDorisParserListener) ExitDropFollowerClause(ctx *DropFollowerClauseContext) {}

// EnterAddBrokerClause is called when production addBrokerClause is entered.
func (s *BaseDorisParserListener) EnterAddBrokerClause(ctx *AddBrokerClauseContext) {}

// ExitAddBrokerClause is called when production addBrokerClause is exited.
func (s *BaseDorisParserListener) ExitAddBrokerClause(ctx *AddBrokerClauseContext) {}

// EnterDropBrokerClause is called when production dropBrokerClause is entered.
func (s *BaseDorisParserListener) EnterDropBrokerClause(ctx *DropBrokerClauseContext) {}

// ExitDropBrokerClause is called when production dropBrokerClause is exited.
func (s *BaseDorisParserListener) ExitDropBrokerClause(ctx *DropBrokerClauseContext) {}

// EnterDropAllBrokerClause is called when production dropAllBrokerClause is entered.
func (s *BaseDorisParserListener) EnterDropAllBrokerClause(ctx *DropAllBrokerClauseContext) {}

// ExitDropAllBrokerClause is called when production dropAllBrokerClause is exited.
func (s *BaseDorisParserListener) ExitDropAllBrokerClause(ctx *DropAllBrokerClauseContext) {}

// EnterAlterLoadErrorUrlClause is called when production alterLoadErrorUrlClause is entered.
func (s *BaseDorisParserListener) EnterAlterLoadErrorUrlClause(ctx *AlterLoadErrorUrlClauseContext) {}

// ExitAlterLoadErrorUrlClause is called when production alterLoadErrorUrlClause is exited.
func (s *BaseDorisParserListener) ExitAlterLoadErrorUrlClause(ctx *AlterLoadErrorUrlClauseContext) {}

// EnterModifyBackendClause is called when production modifyBackendClause is entered.
func (s *BaseDorisParserListener) EnterModifyBackendClause(ctx *ModifyBackendClauseContext) {}

// ExitModifyBackendClause is called when production modifyBackendClause is exited.
func (s *BaseDorisParserListener) ExitModifyBackendClause(ctx *ModifyBackendClauseContext) {}

// EnterModifyFrontendOrBackendHostNameClause is called when production modifyFrontendOrBackendHostNameClause is entered.
func (s *BaseDorisParserListener) EnterModifyFrontendOrBackendHostNameClause(ctx *ModifyFrontendOrBackendHostNameClauseContext) {
}

// ExitModifyFrontendOrBackendHostNameClause is called when production modifyFrontendOrBackendHostNameClause is exited.
func (s *BaseDorisParserListener) ExitModifyFrontendOrBackendHostNameClause(ctx *ModifyFrontendOrBackendHostNameClauseContext) {
}

// EnterDropRollupClause is called when production dropRollupClause is entered.
func (s *BaseDorisParserListener) EnterDropRollupClause(ctx *DropRollupClauseContext) {}

// ExitDropRollupClause is called when production dropRollupClause is exited.
func (s *BaseDorisParserListener) ExitDropRollupClause(ctx *DropRollupClauseContext) {}

// EnterAddRollupClause is called when production addRollupClause is entered.
func (s *BaseDorisParserListener) EnterAddRollupClause(ctx *AddRollupClauseContext) {}

// ExitAddRollupClause is called when production addRollupClause is exited.
func (s *BaseDorisParserListener) ExitAddRollupClause(ctx *AddRollupClauseContext) {}

// EnterAddColumnClause is called when production addColumnClause is entered.
func (s *BaseDorisParserListener) EnterAddColumnClause(ctx *AddColumnClauseContext) {}

// ExitAddColumnClause is called when production addColumnClause is exited.
func (s *BaseDorisParserListener) ExitAddColumnClause(ctx *AddColumnClauseContext) {}

// EnterAddColumnsClause is called when production addColumnsClause is entered.
func (s *BaseDorisParserListener) EnterAddColumnsClause(ctx *AddColumnsClauseContext) {}

// ExitAddColumnsClause is called when production addColumnsClause is exited.
func (s *BaseDorisParserListener) ExitAddColumnsClause(ctx *AddColumnsClauseContext) {}

// EnterDropColumnClause is called when production dropColumnClause is entered.
func (s *BaseDorisParserListener) EnterDropColumnClause(ctx *DropColumnClauseContext) {}

// ExitDropColumnClause is called when production dropColumnClause is exited.
func (s *BaseDorisParserListener) ExitDropColumnClause(ctx *DropColumnClauseContext) {}

// EnterModifyColumnClause is called when production modifyColumnClause is entered.
func (s *BaseDorisParserListener) EnterModifyColumnClause(ctx *ModifyColumnClauseContext) {}

// ExitModifyColumnClause is called when production modifyColumnClause is exited.
func (s *BaseDorisParserListener) ExitModifyColumnClause(ctx *ModifyColumnClauseContext) {}

// EnterReorderColumnsClause is called when production reorderColumnsClause is entered.
func (s *BaseDorisParserListener) EnterReorderColumnsClause(ctx *ReorderColumnsClauseContext) {}

// ExitReorderColumnsClause is called when production reorderColumnsClause is exited.
func (s *BaseDorisParserListener) ExitReorderColumnsClause(ctx *ReorderColumnsClauseContext) {}

// EnterAddPartitionClause is called when production addPartitionClause is entered.
func (s *BaseDorisParserListener) EnterAddPartitionClause(ctx *AddPartitionClauseContext) {}

// ExitAddPartitionClause is called when production addPartitionClause is exited.
func (s *BaseDorisParserListener) ExitAddPartitionClause(ctx *AddPartitionClauseContext) {}

// EnterDropPartitionClause is called when production dropPartitionClause is entered.
func (s *BaseDorisParserListener) EnterDropPartitionClause(ctx *DropPartitionClauseContext) {}

// ExitDropPartitionClause is called when production dropPartitionClause is exited.
func (s *BaseDorisParserListener) ExitDropPartitionClause(ctx *DropPartitionClauseContext) {}

// EnterModifyPartitionClause is called when production modifyPartitionClause is entered.
func (s *BaseDorisParserListener) EnterModifyPartitionClause(ctx *ModifyPartitionClauseContext) {}

// ExitModifyPartitionClause is called when production modifyPartitionClause is exited.
func (s *BaseDorisParserListener) ExitModifyPartitionClause(ctx *ModifyPartitionClauseContext) {}

// EnterReplacePartitionClause is called when production replacePartitionClause is entered.
func (s *BaseDorisParserListener) EnterReplacePartitionClause(ctx *ReplacePartitionClauseContext) {}

// ExitReplacePartitionClause is called when production replacePartitionClause is exited.
func (s *BaseDorisParserListener) ExitReplacePartitionClause(ctx *ReplacePartitionClauseContext) {}

// EnterReplaceTableClause is called when production replaceTableClause is entered.
func (s *BaseDorisParserListener) EnterReplaceTableClause(ctx *ReplaceTableClauseContext) {}

// ExitReplaceTableClause is called when production replaceTableClause is exited.
func (s *BaseDorisParserListener) ExitReplaceTableClause(ctx *ReplaceTableClauseContext) {}

// EnterRenameClause is called when production renameClause is entered.
func (s *BaseDorisParserListener) EnterRenameClause(ctx *RenameClauseContext) {}

// ExitRenameClause is called when production renameClause is exited.
func (s *BaseDorisParserListener) ExitRenameClause(ctx *RenameClauseContext) {}

// EnterRenameRollupClause is called when production renameRollupClause is entered.
func (s *BaseDorisParserListener) EnterRenameRollupClause(ctx *RenameRollupClauseContext) {}

// ExitRenameRollupClause is called when production renameRollupClause is exited.
func (s *BaseDorisParserListener) ExitRenameRollupClause(ctx *RenameRollupClauseContext) {}

// EnterRenamePartitionClause is called when production renamePartitionClause is entered.
func (s *BaseDorisParserListener) EnterRenamePartitionClause(ctx *RenamePartitionClauseContext) {}

// ExitRenamePartitionClause is called when production renamePartitionClause is exited.
func (s *BaseDorisParserListener) ExitRenamePartitionClause(ctx *RenamePartitionClauseContext) {}

// EnterRenameColumnClause is called when production renameColumnClause is entered.
func (s *BaseDorisParserListener) EnterRenameColumnClause(ctx *RenameColumnClauseContext) {}

// ExitRenameColumnClause is called when production renameColumnClause is exited.
func (s *BaseDorisParserListener) ExitRenameColumnClause(ctx *RenameColumnClauseContext) {}

// EnterAddIndexClause is called when production addIndexClause is entered.
func (s *BaseDorisParserListener) EnterAddIndexClause(ctx *AddIndexClauseContext) {}

// ExitAddIndexClause is called when production addIndexClause is exited.
func (s *BaseDorisParserListener) ExitAddIndexClause(ctx *AddIndexClauseContext) {}

// EnterDropIndexClause is called when production dropIndexClause is entered.
func (s *BaseDorisParserListener) EnterDropIndexClause(ctx *DropIndexClauseContext) {}

// ExitDropIndexClause is called when production dropIndexClause is exited.
func (s *BaseDorisParserListener) ExitDropIndexClause(ctx *DropIndexClauseContext) {}

// EnterEnableFeatureClause is called when production enableFeatureClause is entered.
func (s *BaseDorisParserListener) EnterEnableFeatureClause(ctx *EnableFeatureClauseContext) {}

// ExitEnableFeatureClause is called when production enableFeatureClause is exited.
func (s *BaseDorisParserListener) ExitEnableFeatureClause(ctx *EnableFeatureClauseContext) {}

// EnterModifyDistributionClause is called when production modifyDistributionClause is entered.
func (s *BaseDorisParserListener) EnterModifyDistributionClause(ctx *ModifyDistributionClauseContext) {
}

// ExitModifyDistributionClause is called when production modifyDistributionClause is exited.
func (s *BaseDorisParserListener) ExitModifyDistributionClause(ctx *ModifyDistributionClauseContext) {
}

// EnterModifyTableCommentClause is called when production modifyTableCommentClause is entered.
func (s *BaseDorisParserListener) EnterModifyTableCommentClause(ctx *ModifyTableCommentClauseContext) {
}

// ExitModifyTableCommentClause is called when production modifyTableCommentClause is exited.
func (s *BaseDorisParserListener) ExitModifyTableCommentClause(ctx *ModifyTableCommentClauseContext) {
}

// EnterModifyColumnCommentClause is called when production modifyColumnCommentClause is entered.
func (s *BaseDorisParserListener) EnterModifyColumnCommentClause(ctx *ModifyColumnCommentClauseContext) {
}

// ExitModifyColumnCommentClause is called when production modifyColumnCommentClause is exited.
func (s *BaseDorisParserListener) ExitModifyColumnCommentClause(ctx *ModifyColumnCommentClauseContext) {
}

// EnterModifyEngineClause is called when production modifyEngineClause is entered.
func (s *BaseDorisParserListener) EnterModifyEngineClause(ctx *ModifyEngineClauseContext) {}

// ExitModifyEngineClause is called when production modifyEngineClause is exited.
func (s *BaseDorisParserListener) ExitModifyEngineClause(ctx *ModifyEngineClauseContext) {}

// EnterAlterMultiPartitionClause is called when production alterMultiPartitionClause is entered.
func (s *BaseDorisParserListener) EnterAlterMultiPartitionClause(ctx *AlterMultiPartitionClauseContext) {
}

// ExitAlterMultiPartitionClause is called when production alterMultiPartitionClause is exited.
func (s *BaseDorisParserListener) ExitAlterMultiPartitionClause(ctx *AlterMultiPartitionClauseContext) {
}

// EnterCreateOrReplaceTagClauses is called when production createOrReplaceTagClauses is entered.
func (s *BaseDorisParserListener) EnterCreateOrReplaceTagClauses(ctx *CreateOrReplaceTagClausesContext) {
}

// ExitCreateOrReplaceTagClauses is called when production createOrReplaceTagClauses is exited.
func (s *BaseDorisParserListener) ExitCreateOrReplaceTagClauses(ctx *CreateOrReplaceTagClausesContext) {
}

// EnterCreateOrReplaceBranchClauses is called when production createOrReplaceBranchClauses is entered.
func (s *BaseDorisParserListener) EnterCreateOrReplaceBranchClauses(ctx *CreateOrReplaceBranchClausesContext) {
}

// ExitCreateOrReplaceBranchClauses is called when production createOrReplaceBranchClauses is exited.
func (s *BaseDorisParserListener) ExitCreateOrReplaceBranchClauses(ctx *CreateOrReplaceBranchClausesContext) {
}

// EnterDropBranchClauses is called when production dropBranchClauses is entered.
func (s *BaseDorisParserListener) EnterDropBranchClauses(ctx *DropBranchClausesContext) {}

// ExitDropBranchClauses is called when production dropBranchClauses is exited.
func (s *BaseDorisParserListener) ExitDropBranchClauses(ctx *DropBranchClausesContext) {}

// EnterDropTagClauses is called when production dropTagClauses is entered.
func (s *BaseDorisParserListener) EnterDropTagClauses(ctx *DropTagClausesContext) {}

// ExitDropTagClauses is called when production dropTagClauses is exited.
func (s *BaseDorisParserListener) ExitDropTagClauses(ctx *DropTagClausesContext) {}

// EnterCreateOrReplaceTagClause is called when production createOrReplaceTagClause is entered.
func (s *BaseDorisParserListener) EnterCreateOrReplaceTagClause(ctx *CreateOrReplaceTagClauseContext) {
}

// ExitCreateOrReplaceTagClause is called when production createOrReplaceTagClause is exited.
func (s *BaseDorisParserListener) ExitCreateOrReplaceTagClause(ctx *CreateOrReplaceTagClauseContext) {
}

// EnterCreateOrReplaceBranchClause is called when production createOrReplaceBranchClause is entered.
func (s *BaseDorisParserListener) EnterCreateOrReplaceBranchClause(ctx *CreateOrReplaceBranchClauseContext) {
}

// ExitCreateOrReplaceBranchClause is called when production createOrReplaceBranchClause is exited.
func (s *BaseDorisParserListener) ExitCreateOrReplaceBranchClause(ctx *CreateOrReplaceBranchClauseContext) {
}

// EnterTagOptions is called when production tagOptions is entered.
func (s *BaseDorisParserListener) EnterTagOptions(ctx *TagOptionsContext) {}

// ExitTagOptions is called when production tagOptions is exited.
func (s *BaseDorisParserListener) ExitTagOptions(ctx *TagOptionsContext) {}

// EnterBranchOptions is called when production branchOptions is entered.
func (s *BaseDorisParserListener) EnterBranchOptions(ctx *BranchOptionsContext) {}

// ExitBranchOptions is called when production branchOptions is exited.
func (s *BaseDorisParserListener) ExitBranchOptions(ctx *BranchOptionsContext) {}

// EnterRetainTime is called when production retainTime is entered.
func (s *BaseDorisParserListener) EnterRetainTime(ctx *RetainTimeContext) {}

// ExitRetainTime is called when production retainTime is exited.
func (s *BaseDorisParserListener) ExitRetainTime(ctx *RetainTimeContext) {}

// EnterRetentionSnapshot is called when production retentionSnapshot is entered.
func (s *BaseDorisParserListener) EnterRetentionSnapshot(ctx *RetentionSnapshotContext) {}

// ExitRetentionSnapshot is called when production retentionSnapshot is exited.
func (s *BaseDorisParserListener) ExitRetentionSnapshot(ctx *RetentionSnapshotContext) {}

// EnterMinSnapshotsToKeep is called when production minSnapshotsToKeep is entered.
func (s *BaseDorisParserListener) EnterMinSnapshotsToKeep(ctx *MinSnapshotsToKeepContext) {}

// ExitMinSnapshotsToKeep is called when production minSnapshotsToKeep is exited.
func (s *BaseDorisParserListener) ExitMinSnapshotsToKeep(ctx *MinSnapshotsToKeepContext) {}

// EnterTimeValueWithUnit is called when production timeValueWithUnit is entered.
func (s *BaseDorisParserListener) EnterTimeValueWithUnit(ctx *TimeValueWithUnitContext) {}

// ExitTimeValueWithUnit is called when production timeValueWithUnit is exited.
func (s *BaseDorisParserListener) ExitTimeValueWithUnit(ctx *TimeValueWithUnitContext) {}

// EnterDropBranchClause is called when production dropBranchClause is entered.
func (s *BaseDorisParserListener) EnterDropBranchClause(ctx *DropBranchClauseContext) {}

// ExitDropBranchClause is called when production dropBranchClause is exited.
func (s *BaseDorisParserListener) ExitDropBranchClause(ctx *DropBranchClauseContext) {}

// EnterDropTagClause is called when production dropTagClause is entered.
func (s *BaseDorisParserListener) EnterDropTagClause(ctx *DropTagClauseContext) {}

// ExitDropTagClause is called when production dropTagClause is exited.
func (s *BaseDorisParserListener) ExitDropTagClause(ctx *DropTagClauseContext) {}

// EnterColumnPosition is called when production columnPosition is entered.
func (s *BaseDorisParserListener) EnterColumnPosition(ctx *ColumnPositionContext) {}

// ExitColumnPosition is called when production columnPosition is exited.
func (s *BaseDorisParserListener) ExitColumnPosition(ctx *ColumnPositionContext) {}

// EnterToRollup is called when production toRollup is entered.
func (s *BaseDorisParserListener) EnterToRollup(ctx *ToRollupContext) {}

// ExitToRollup is called when production toRollup is exited.
func (s *BaseDorisParserListener) ExitToRollup(ctx *ToRollupContext) {}

// EnterFromRollup is called when production fromRollup is entered.
func (s *BaseDorisParserListener) EnterFromRollup(ctx *FromRollupContext) {}

// ExitFromRollup is called when production fromRollup is exited.
func (s *BaseDorisParserListener) ExitFromRollup(ctx *FromRollupContext) {}

// EnterShowAnalyze is called when production showAnalyze is entered.
func (s *BaseDorisParserListener) EnterShowAnalyze(ctx *ShowAnalyzeContext) {}

// ExitShowAnalyze is called when production showAnalyze is exited.
func (s *BaseDorisParserListener) ExitShowAnalyze(ctx *ShowAnalyzeContext) {}

// EnterShowQueuedAnalyzeJobs is called when production showQueuedAnalyzeJobs is entered.
func (s *BaseDorisParserListener) EnterShowQueuedAnalyzeJobs(ctx *ShowQueuedAnalyzeJobsContext) {}

// ExitShowQueuedAnalyzeJobs is called when production showQueuedAnalyzeJobs is exited.
func (s *BaseDorisParserListener) ExitShowQueuedAnalyzeJobs(ctx *ShowQueuedAnalyzeJobsContext) {}

// EnterShowColumnHistogramStats is called when production showColumnHistogramStats is entered.
func (s *BaseDorisParserListener) EnterShowColumnHistogramStats(ctx *ShowColumnHistogramStatsContext) {
}

// ExitShowColumnHistogramStats is called when production showColumnHistogramStats is exited.
func (s *BaseDorisParserListener) ExitShowColumnHistogramStats(ctx *ShowColumnHistogramStatsContext) {
}

// EnterAnalyzeDatabase is called when production analyzeDatabase is entered.
func (s *BaseDorisParserListener) EnterAnalyzeDatabase(ctx *AnalyzeDatabaseContext) {}

// ExitAnalyzeDatabase is called when production analyzeDatabase is exited.
func (s *BaseDorisParserListener) ExitAnalyzeDatabase(ctx *AnalyzeDatabaseContext) {}

// EnterAnalyzeTable is called when production analyzeTable is entered.
func (s *BaseDorisParserListener) EnterAnalyzeTable(ctx *AnalyzeTableContext) {}

// ExitAnalyzeTable is called when production analyzeTable is exited.
func (s *BaseDorisParserListener) ExitAnalyzeTable(ctx *AnalyzeTableContext) {}

// EnterAlterTableStats is called when production alterTableStats is entered.
func (s *BaseDorisParserListener) EnterAlterTableStats(ctx *AlterTableStatsContext) {}

// ExitAlterTableStats is called when production alterTableStats is exited.
func (s *BaseDorisParserListener) ExitAlterTableStats(ctx *AlterTableStatsContext) {}

// EnterAlterColumnStats is called when production alterColumnStats is entered.
func (s *BaseDorisParserListener) EnterAlterColumnStats(ctx *AlterColumnStatsContext) {}

// ExitAlterColumnStats is called when production alterColumnStats is exited.
func (s *BaseDorisParserListener) ExitAlterColumnStats(ctx *AlterColumnStatsContext) {}

// EnterShowIndexStats is called when production showIndexStats is entered.
func (s *BaseDorisParserListener) EnterShowIndexStats(ctx *ShowIndexStatsContext) {}

// ExitShowIndexStats is called when production showIndexStats is exited.
func (s *BaseDorisParserListener) ExitShowIndexStats(ctx *ShowIndexStatsContext) {}

// EnterDropStats is called when production dropStats is entered.
func (s *BaseDorisParserListener) EnterDropStats(ctx *DropStatsContext) {}

// ExitDropStats is called when production dropStats is exited.
func (s *BaseDorisParserListener) ExitDropStats(ctx *DropStatsContext) {}

// EnterDropCachedStats is called when production dropCachedStats is entered.
func (s *BaseDorisParserListener) EnterDropCachedStats(ctx *DropCachedStatsContext) {}

// ExitDropCachedStats is called when production dropCachedStats is exited.
func (s *BaseDorisParserListener) ExitDropCachedStats(ctx *DropCachedStatsContext) {}

// EnterDropExpiredStats is called when production dropExpiredStats is entered.
func (s *BaseDorisParserListener) EnterDropExpiredStats(ctx *DropExpiredStatsContext) {}

// ExitDropExpiredStats is called when production dropExpiredStats is exited.
func (s *BaseDorisParserListener) ExitDropExpiredStats(ctx *DropExpiredStatsContext) {}

// EnterKillAnalyzeJob is called when production killAnalyzeJob is entered.
func (s *BaseDorisParserListener) EnterKillAnalyzeJob(ctx *KillAnalyzeJobContext) {}

// ExitKillAnalyzeJob is called when production killAnalyzeJob is exited.
func (s *BaseDorisParserListener) ExitKillAnalyzeJob(ctx *KillAnalyzeJobContext) {}

// EnterDropAnalyzeJob is called when production dropAnalyzeJob is entered.
func (s *BaseDorisParserListener) EnterDropAnalyzeJob(ctx *DropAnalyzeJobContext) {}

// ExitDropAnalyzeJob is called when production dropAnalyzeJob is exited.
func (s *BaseDorisParserListener) ExitDropAnalyzeJob(ctx *DropAnalyzeJobContext) {}

// EnterShowTableStats is called when production showTableStats is entered.
func (s *BaseDorisParserListener) EnterShowTableStats(ctx *ShowTableStatsContext) {}

// ExitShowTableStats is called when production showTableStats is exited.
func (s *BaseDorisParserListener) ExitShowTableStats(ctx *ShowTableStatsContext) {}

// EnterShowColumnStats is called when production showColumnStats is entered.
func (s *BaseDorisParserListener) EnterShowColumnStats(ctx *ShowColumnStatsContext) {}

// ExitShowColumnStats is called when production showColumnStats is exited.
func (s *BaseDorisParserListener) ExitShowColumnStats(ctx *ShowColumnStatsContext) {}

// EnterShowAnalyzeTask is called when production showAnalyzeTask is entered.
func (s *BaseDorisParserListener) EnterShowAnalyzeTask(ctx *ShowAnalyzeTaskContext) {}

// ExitShowAnalyzeTask is called when production showAnalyzeTask is exited.
func (s *BaseDorisParserListener) ExitShowAnalyzeTask(ctx *ShowAnalyzeTaskContext) {}

// EnterAnalyzeProperties is called when production analyzeProperties is entered.
func (s *BaseDorisParserListener) EnterAnalyzeProperties(ctx *AnalyzePropertiesContext) {}

// ExitAnalyzeProperties is called when production analyzeProperties is exited.
func (s *BaseDorisParserListener) ExitAnalyzeProperties(ctx *AnalyzePropertiesContext) {}

// EnterWorkloadPolicyActions is called when production workloadPolicyActions is entered.
func (s *BaseDorisParserListener) EnterWorkloadPolicyActions(ctx *WorkloadPolicyActionsContext) {}

// ExitWorkloadPolicyActions is called when production workloadPolicyActions is exited.
func (s *BaseDorisParserListener) ExitWorkloadPolicyActions(ctx *WorkloadPolicyActionsContext) {}

// EnterWorkloadPolicyAction is called when production workloadPolicyAction is entered.
func (s *BaseDorisParserListener) EnterWorkloadPolicyAction(ctx *WorkloadPolicyActionContext) {}

// ExitWorkloadPolicyAction is called when production workloadPolicyAction is exited.
func (s *BaseDorisParserListener) ExitWorkloadPolicyAction(ctx *WorkloadPolicyActionContext) {}

// EnterWorkloadPolicyConditions is called when production workloadPolicyConditions is entered.
func (s *BaseDorisParserListener) EnterWorkloadPolicyConditions(ctx *WorkloadPolicyConditionsContext) {
}

// ExitWorkloadPolicyConditions is called when production workloadPolicyConditions is exited.
func (s *BaseDorisParserListener) ExitWorkloadPolicyConditions(ctx *WorkloadPolicyConditionsContext) {
}

// EnterWorkloadPolicyCondition is called when production workloadPolicyCondition is entered.
func (s *BaseDorisParserListener) EnterWorkloadPolicyCondition(ctx *WorkloadPolicyConditionContext) {}

// ExitWorkloadPolicyCondition is called when production workloadPolicyCondition is exited.
func (s *BaseDorisParserListener) ExitWorkloadPolicyCondition(ctx *WorkloadPolicyConditionContext) {}

// EnterStorageBackend is called when production storageBackend is entered.
func (s *BaseDorisParserListener) EnterStorageBackend(ctx *StorageBackendContext) {}

// ExitStorageBackend is called when production storageBackend is exited.
func (s *BaseDorisParserListener) ExitStorageBackend(ctx *StorageBackendContext) {}

// EnterPasswordOption is called when production passwordOption is entered.
func (s *BaseDorisParserListener) EnterPasswordOption(ctx *PasswordOptionContext) {}

// ExitPasswordOption is called when production passwordOption is exited.
func (s *BaseDorisParserListener) ExitPasswordOption(ctx *PasswordOptionContext) {}

// EnterFunctionArguments is called when production functionArguments is entered.
func (s *BaseDorisParserListener) EnterFunctionArguments(ctx *FunctionArgumentsContext) {}

// ExitFunctionArguments is called when production functionArguments is exited.
func (s *BaseDorisParserListener) ExitFunctionArguments(ctx *FunctionArgumentsContext) {}

// EnterDataTypeList is called when production dataTypeList is entered.
func (s *BaseDorisParserListener) EnterDataTypeList(ctx *DataTypeListContext) {}

// ExitDataTypeList is called when production dataTypeList is exited.
func (s *BaseDorisParserListener) ExitDataTypeList(ctx *DataTypeListContext) {}

// EnterSetOptions is called when production setOptions is entered.
func (s *BaseDorisParserListener) EnterSetOptions(ctx *SetOptionsContext) {}

// ExitSetOptions is called when production setOptions is exited.
func (s *BaseDorisParserListener) ExitSetOptions(ctx *SetOptionsContext) {}

// EnterSetDefaultStorageVault is called when production setDefaultStorageVault is entered.
func (s *BaseDorisParserListener) EnterSetDefaultStorageVault(ctx *SetDefaultStorageVaultContext) {}

// ExitSetDefaultStorageVault is called when production setDefaultStorageVault is exited.
func (s *BaseDorisParserListener) ExitSetDefaultStorageVault(ctx *SetDefaultStorageVaultContext) {}

// EnterSetUserProperties is called when production setUserProperties is entered.
func (s *BaseDorisParserListener) EnterSetUserProperties(ctx *SetUserPropertiesContext) {}

// ExitSetUserProperties is called when production setUserProperties is exited.
func (s *BaseDorisParserListener) ExitSetUserProperties(ctx *SetUserPropertiesContext) {}

// EnterSetTransaction is called when production setTransaction is entered.
func (s *BaseDorisParserListener) EnterSetTransaction(ctx *SetTransactionContext) {}

// ExitSetTransaction is called when production setTransaction is exited.
func (s *BaseDorisParserListener) ExitSetTransaction(ctx *SetTransactionContext) {}

// EnterSetVariableWithType is called when production setVariableWithType is entered.
func (s *BaseDorisParserListener) EnterSetVariableWithType(ctx *SetVariableWithTypeContext) {}

// ExitSetVariableWithType is called when production setVariableWithType is exited.
func (s *BaseDorisParserListener) ExitSetVariableWithType(ctx *SetVariableWithTypeContext) {}

// EnterSetNames is called when production setNames is entered.
func (s *BaseDorisParserListener) EnterSetNames(ctx *SetNamesContext) {}

// ExitSetNames is called when production setNames is exited.
func (s *BaseDorisParserListener) ExitSetNames(ctx *SetNamesContext) {}

// EnterSetCharset is called when production setCharset is entered.
func (s *BaseDorisParserListener) EnterSetCharset(ctx *SetCharsetContext) {}

// ExitSetCharset is called when production setCharset is exited.
func (s *BaseDorisParserListener) ExitSetCharset(ctx *SetCharsetContext) {}

// EnterSetCollate is called when production setCollate is entered.
func (s *BaseDorisParserListener) EnterSetCollate(ctx *SetCollateContext) {}

// ExitSetCollate is called when production setCollate is exited.
func (s *BaseDorisParserListener) ExitSetCollate(ctx *SetCollateContext) {}

// EnterSetPassword is called when production setPassword is entered.
func (s *BaseDorisParserListener) EnterSetPassword(ctx *SetPasswordContext) {}

// ExitSetPassword is called when production setPassword is exited.
func (s *BaseDorisParserListener) ExitSetPassword(ctx *SetPasswordContext) {}

// EnterSetLdapAdminPassword is called when production setLdapAdminPassword is entered.
func (s *BaseDorisParserListener) EnterSetLdapAdminPassword(ctx *SetLdapAdminPasswordContext) {}

// ExitSetLdapAdminPassword is called when production setLdapAdminPassword is exited.
func (s *BaseDorisParserListener) ExitSetLdapAdminPassword(ctx *SetLdapAdminPasswordContext) {}

// EnterSetVariableWithoutType is called when production setVariableWithoutType is entered.
func (s *BaseDorisParserListener) EnterSetVariableWithoutType(ctx *SetVariableWithoutTypeContext) {}

// ExitSetVariableWithoutType is called when production setVariableWithoutType is exited.
func (s *BaseDorisParserListener) ExitSetVariableWithoutType(ctx *SetVariableWithoutTypeContext) {}

// EnterSetSystemVariable is called when production setSystemVariable is entered.
func (s *BaseDorisParserListener) EnterSetSystemVariable(ctx *SetSystemVariableContext) {}

// ExitSetSystemVariable is called when production setSystemVariable is exited.
func (s *BaseDorisParserListener) ExitSetSystemVariable(ctx *SetSystemVariableContext) {}

// EnterSetUserVariable is called when production setUserVariable is entered.
func (s *BaseDorisParserListener) EnterSetUserVariable(ctx *SetUserVariableContext) {}

// ExitSetUserVariable is called when production setUserVariable is exited.
func (s *BaseDorisParserListener) ExitSetUserVariable(ctx *SetUserVariableContext) {}

// EnterTransactionAccessMode is called when production transactionAccessMode is entered.
func (s *BaseDorisParserListener) EnterTransactionAccessMode(ctx *TransactionAccessModeContext) {}

// ExitTransactionAccessMode is called when production transactionAccessMode is exited.
func (s *BaseDorisParserListener) ExitTransactionAccessMode(ctx *TransactionAccessModeContext) {}

// EnterIsolationLevel is called when production isolationLevel is entered.
func (s *BaseDorisParserListener) EnterIsolationLevel(ctx *IsolationLevelContext) {}

// ExitIsolationLevel is called when production isolationLevel is exited.
func (s *BaseDorisParserListener) ExitIsolationLevel(ctx *IsolationLevelContext) {}

// EnterSupportedUnsetStatement is called when production supportedUnsetStatement is entered.
func (s *BaseDorisParserListener) EnterSupportedUnsetStatement(ctx *SupportedUnsetStatementContext) {}

// ExitSupportedUnsetStatement is called when production supportedUnsetStatement is exited.
func (s *BaseDorisParserListener) ExitSupportedUnsetStatement(ctx *SupportedUnsetStatementContext) {}

// EnterSwitchCatalog is called when production switchCatalog is entered.
func (s *BaseDorisParserListener) EnterSwitchCatalog(ctx *SwitchCatalogContext) {}

// ExitSwitchCatalog is called when production switchCatalog is exited.
func (s *BaseDorisParserListener) ExitSwitchCatalog(ctx *SwitchCatalogContext) {}

// EnterUseDatabase is called when production useDatabase is entered.
func (s *BaseDorisParserListener) EnterUseDatabase(ctx *UseDatabaseContext) {}

// ExitUseDatabase is called when production useDatabase is exited.
func (s *BaseDorisParserListener) ExitUseDatabase(ctx *UseDatabaseContext) {}

// EnterUseCloudCluster is called when production useCloudCluster is entered.
func (s *BaseDorisParserListener) EnterUseCloudCluster(ctx *UseCloudClusterContext) {}

// ExitUseCloudCluster is called when production useCloudCluster is exited.
func (s *BaseDorisParserListener) ExitUseCloudCluster(ctx *UseCloudClusterContext) {}

// EnterStageAndPattern is called when production stageAndPattern is entered.
func (s *BaseDorisParserListener) EnterStageAndPattern(ctx *StageAndPatternContext) {}

// ExitStageAndPattern is called when production stageAndPattern is exited.
func (s *BaseDorisParserListener) ExitStageAndPattern(ctx *StageAndPatternContext) {}

// EnterDescribeTableValuedFunction is called when production describeTableValuedFunction is entered.
func (s *BaseDorisParserListener) EnterDescribeTableValuedFunction(ctx *DescribeTableValuedFunctionContext) {
}

// ExitDescribeTableValuedFunction is called when production describeTableValuedFunction is exited.
func (s *BaseDorisParserListener) ExitDescribeTableValuedFunction(ctx *DescribeTableValuedFunctionContext) {
}

// EnterDescribeTableAll is called when production describeTableAll is entered.
func (s *BaseDorisParserListener) EnterDescribeTableAll(ctx *DescribeTableAllContext) {}

// ExitDescribeTableAll is called when production describeTableAll is exited.
func (s *BaseDorisParserListener) ExitDescribeTableAll(ctx *DescribeTableAllContext) {}

// EnterDescribeTable is called when production describeTable is entered.
func (s *BaseDorisParserListener) EnterDescribeTable(ctx *DescribeTableContext) {}

// ExitDescribeTable is called when production describeTable is exited.
func (s *BaseDorisParserListener) ExitDescribeTable(ctx *DescribeTableContext) {}

// EnterDescribeDictionary is called when production describeDictionary is entered.
func (s *BaseDorisParserListener) EnterDescribeDictionary(ctx *DescribeDictionaryContext) {}

// ExitDescribeDictionary is called when production describeDictionary is exited.
func (s *BaseDorisParserListener) ExitDescribeDictionary(ctx *DescribeDictionaryContext) {}

// EnterConstraint is called when production constraint is entered.
func (s *BaseDorisParserListener) EnterConstraint(ctx *ConstraintContext) {}

// ExitConstraint is called when production constraint is exited.
func (s *BaseDorisParserListener) ExitConstraint(ctx *ConstraintContext) {}

// EnterPartitionSpec is called when production partitionSpec is entered.
func (s *BaseDorisParserListener) EnterPartitionSpec(ctx *PartitionSpecContext) {}

// ExitPartitionSpec is called when production partitionSpec is exited.
func (s *BaseDorisParserListener) ExitPartitionSpec(ctx *PartitionSpecContext) {}

// EnterPartitionTable is called when production partitionTable is entered.
func (s *BaseDorisParserListener) EnterPartitionTable(ctx *PartitionTableContext) {}

// ExitPartitionTable is called when production partitionTable is exited.
func (s *BaseDorisParserListener) ExitPartitionTable(ctx *PartitionTableContext) {}

// EnterIdentityOrFunctionList is called when production identityOrFunctionList is entered.
func (s *BaseDorisParserListener) EnterIdentityOrFunctionList(ctx *IdentityOrFunctionListContext) {}

// ExitIdentityOrFunctionList is called when production identityOrFunctionList is exited.
func (s *BaseDorisParserListener) ExitIdentityOrFunctionList(ctx *IdentityOrFunctionListContext) {}

// EnterIdentityOrFunction is called when production identityOrFunction is entered.
func (s *BaseDorisParserListener) EnterIdentityOrFunction(ctx *IdentityOrFunctionContext) {}

// ExitIdentityOrFunction is called when production identityOrFunction is exited.
func (s *BaseDorisParserListener) ExitIdentityOrFunction(ctx *IdentityOrFunctionContext) {}

// EnterDataDesc is called when production dataDesc is entered.
func (s *BaseDorisParserListener) EnterDataDesc(ctx *DataDescContext) {}

// ExitDataDesc is called when production dataDesc is exited.
func (s *BaseDorisParserListener) ExitDataDesc(ctx *DataDescContext) {}

// EnterStatementScope is called when production statementScope is entered.
func (s *BaseDorisParserListener) EnterStatementScope(ctx *StatementScopeContext) {}

// ExitStatementScope is called when production statementScope is exited.
func (s *BaseDorisParserListener) ExitStatementScope(ctx *StatementScopeContext) {}

// EnterBuildMode is called when production buildMode is entered.
func (s *BaseDorisParserListener) EnterBuildMode(ctx *BuildModeContext) {}

// ExitBuildMode is called when production buildMode is exited.
func (s *BaseDorisParserListener) ExitBuildMode(ctx *BuildModeContext) {}

// EnterRefreshTrigger is called when production refreshTrigger is entered.
func (s *BaseDorisParserListener) EnterRefreshTrigger(ctx *RefreshTriggerContext) {}

// ExitRefreshTrigger is called when production refreshTrigger is exited.
func (s *BaseDorisParserListener) ExitRefreshTrigger(ctx *RefreshTriggerContext) {}

// EnterRefreshSchedule is called when production refreshSchedule is entered.
func (s *BaseDorisParserListener) EnterRefreshSchedule(ctx *RefreshScheduleContext) {}

// ExitRefreshSchedule is called when production refreshSchedule is exited.
func (s *BaseDorisParserListener) ExitRefreshSchedule(ctx *RefreshScheduleContext) {}

// EnterRefreshMethod is called when production refreshMethod is entered.
func (s *BaseDorisParserListener) EnterRefreshMethod(ctx *RefreshMethodContext) {}

// ExitRefreshMethod is called when production refreshMethod is exited.
func (s *BaseDorisParserListener) ExitRefreshMethod(ctx *RefreshMethodContext) {}

// EnterMvPartition is called when production mvPartition is entered.
func (s *BaseDorisParserListener) EnterMvPartition(ctx *MvPartitionContext) {}

// ExitMvPartition is called when production mvPartition is exited.
func (s *BaseDorisParserListener) ExitMvPartition(ctx *MvPartitionContext) {}

// EnterIdentifierOrText is called when production identifierOrText is entered.
func (s *BaseDorisParserListener) EnterIdentifierOrText(ctx *IdentifierOrTextContext) {}

// ExitIdentifierOrText is called when production identifierOrText is exited.
func (s *BaseDorisParserListener) ExitIdentifierOrText(ctx *IdentifierOrTextContext) {}

// EnterIdentifierOrTextOrAsterisk is called when production identifierOrTextOrAsterisk is entered.
func (s *BaseDorisParserListener) EnterIdentifierOrTextOrAsterisk(ctx *IdentifierOrTextOrAsteriskContext) {
}

// ExitIdentifierOrTextOrAsterisk is called when production identifierOrTextOrAsterisk is exited.
func (s *BaseDorisParserListener) ExitIdentifierOrTextOrAsterisk(ctx *IdentifierOrTextOrAsteriskContext) {
}

// EnterMultipartIdentifierOrAsterisk is called when production multipartIdentifierOrAsterisk is entered.
func (s *BaseDorisParserListener) EnterMultipartIdentifierOrAsterisk(ctx *MultipartIdentifierOrAsteriskContext) {
}

// ExitMultipartIdentifierOrAsterisk is called when production multipartIdentifierOrAsterisk is exited.
func (s *BaseDorisParserListener) ExitMultipartIdentifierOrAsterisk(ctx *MultipartIdentifierOrAsteriskContext) {
}

// EnterIdentifierOrAsterisk is called when production identifierOrAsterisk is entered.
func (s *BaseDorisParserListener) EnterIdentifierOrAsterisk(ctx *IdentifierOrAsteriskContext) {}

// ExitIdentifierOrAsterisk is called when production identifierOrAsterisk is exited.
func (s *BaseDorisParserListener) ExitIdentifierOrAsterisk(ctx *IdentifierOrAsteriskContext) {}

// EnterUserIdentify is called when production userIdentify is entered.
func (s *BaseDorisParserListener) EnterUserIdentify(ctx *UserIdentifyContext) {}

// ExitUserIdentify is called when production userIdentify is exited.
func (s *BaseDorisParserListener) ExitUserIdentify(ctx *UserIdentifyContext) {}

// EnterGrantUserIdentify is called when production grantUserIdentify is entered.
func (s *BaseDorisParserListener) EnterGrantUserIdentify(ctx *GrantUserIdentifyContext) {}

// ExitGrantUserIdentify is called when production grantUserIdentify is exited.
func (s *BaseDorisParserListener) ExitGrantUserIdentify(ctx *GrantUserIdentifyContext) {}

// EnterExplain is called when production explain is entered.
func (s *BaseDorisParserListener) EnterExplain(ctx *ExplainContext) {}

// ExitExplain is called when production explain is exited.
func (s *BaseDorisParserListener) ExitExplain(ctx *ExplainContext) {}

// EnterExplainCommand is called when production explainCommand is entered.
func (s *BaseDorisParserListener) EnterExplainCommand(ctx *ExplainCommandContext) {}

// ExitExplainCommand is called when production explainCommand is exited.
func (s *BaseDorisParserListener) ExitExplainCommand(ctx *ExplainCommandContext) {}

// EnterPlanType is called when production planType is entered.
func (s *BaseDorisParserListener) EnterPlanType(ctx *PlanTypeContext) {}

// ExitPlanType is called when production planType is exited.
func (s *BaseDorisParserListener) ExitPlanType(ctx *PlanTypeContext) {}

// EnterReplayCommand is called when production replayCommand is entered.
func (s *BaseDorisParserListener) EnterReplayCommand(ctx *ReplayCommandContext) {}

// ExitReplayCommand is called when production replayCommand is exited.
func (s *BaseDorisParserListener) ExitReplayCommand(ctx *ReplayCommandContext) {}

// EnterReplayType is called when production replayType is entered.
func (s *BaseDorisParserListener) EnterReplayType(ctx *ReplayTypeContext) {}

// ExitReplayType is called when production replayType is exited.
func (s *BaseDorisParserListener) ExitReplayType(ctx *ReplayTypeContext) {}

// EnterMergeType is called when production mergeType is entered.
func (s *BaseDorisParserListener) EnterMergeType(ctx *MergeTypeContext) {}

// ExitMergeType is called when production mergeType is exited.
func (s *BaseDorisParserListener) ExitMergeType(ctx *MergeTypeContext) {}

// EnterPreFilterClause is called when production preFilterClause is entered.
func (s *BaseDorisParserListener) EnterPreFilterClause(ctx *PreFilterClauseContext) {}

// ExitPreFilterClause is called when production preFilterClause is exited.
func (s *BaseDorisParserListener) ExitPreFilterClause(ctx *PreFilterClauseContext) {}

// EnterDeleteOnClause is called when production deleteOnClause is entered.
func (s *BaseDorisParserListener) EnterDeleteOnClause(ctx *DeleteOnClauseContext) {}

// ExitDeleteOnClause is called when production deleteOnClause is exited.
func (s *BaseDorisParserListener) ExitDeleteOnClause(ctx *DeleteOnClauseContext) {}

// EnterSequenceColClause is called when production sequenceColClause is entered.
func (s *BaseDorisParserListener) EnterSequenceColClause(ctx *SequenceColClauseContext) {}

// ExitSequenceColClause is called when production sequenceColClause is exited.
func (s *BaseDorisParserListener) ExitSequenceColClause(ctx *SequenceColClauseContext) {}

// EnterColFromPath is called when production colFromPath is entered.
func (s *BaseDorisParserListener) EnterColFromPath(ctx *ColFromPathContext) {}

// ExitColFromPath is called when production colFromPath is exited.
func (s *BaseDorisParserListener) ExitColFromPath(ctx *ColFromPathContext) {}

// EnterColMappingList is called when production colMappingList is entered.
func (s *BaseDorisParserListener) EnterColMappingList(ctx *ColMappingListContext) {}

// ExitColMappingList is called when production colMappingList is exited.
func (s *BaseDorisParserListener) ExitColMappingList(ctx *ColMappingListContext) {}

// EnterMappingExpr is called when production mappingExpr is entered.
func (s *BaseDorisParserListener) EnterMappingExpr(ctx *MappingExprContext) {}

// ExitMappingExpr is called when production mappingExpr is exited.
func (s *BaseDorisParserListener) ExitMappingExpr(ctx *MappingExprContext) {}

// EnterWithRemoteStorageSystem is called when production withRemoteStorageSystem is entered.
func (s *BaseDorisParserListener) EnterWithRemoteStorageSystem(ctx *WithRemoteStorageSystemContext) {}

// ExitWithRemoteStorageSystem is called when production withRemoteStorageSystem is exited.
func (s *BaseDorisParserListener) ExitWithRemoteStorageSystem(ctx *WithRemoteStorageSystemContext) {}

// EnterResourceDesc is called when production resourceDesc is entered.
func (s *BaseDorisParserListener) EnterResourceDesc(ctx *ResourceDescContext) {}

// ExitResourceDesc is called when production resourceDesc is exited.
func (s *BaseDorisParserListener) ExitResourceDesc(ctx *ResourceDescContext) {}

// EnterMysqlDataDesc is called when production mysqlDataDesc is entered.
func (s *BaseDorisParserListener) EnterMysqlDataDesc(ctx *MysqlDataDescContext) {}

// ExitMysqlDataDesc is called when production mysqlDataDesc is exited.
func (s *BaseDorisParserListener) ExitMysqlDataDesc(ctx *MysqlDataDescContext) {}

// EnterSkipLines is called when production skipLines is entered.
func (s *BaseDorisParserListener) EnterSkipLines(ctx *SkipLinesContext) {}

// ExitSkipLines is called when production skipLines is exited.
func (s *BaseDorisParserListener) ExitSkipLines(ctx *SkipLinesContext) {}

// EnterOutFileClause is called when production outFileClause is entered.
func (s *BaseDorisParserListener) EnterOutFileClause(ctx *OutFileClauseContext) {}

// ExitOutFileClause is called when production outFileClause is exited.
func (s *BaseDorisParserListener) ExitOutFileClause(ctx *OutFileClauseContext) {}

// EnterQuery is called when production query is entered.
func (s *BaseDorisParserListener) EnterQuery(ctx *QueryContext) {}

// ExitQuery is called when production query is exited.
func (s *BaseDorisParserListener) ExitQuery(ctx *QueryContext) {}

// EnterQueryTermDefault is called when production queryTermDefault is entered.
func (s *BaseDorisParserListener) EnterQueryTermDefault(ctx *QueryTermDefaultContext) {}

// ExitQueryTermDefault is called when production queryTermDefault is exited.
func (s *BaseDorisParserListener) ExitQueryTermDefault(ctx *QueryTermDefaultContext) {}

// EnterSetOperation is called when production setOperation is entered.
func (s *BaseDorisParserListener) EnterSetOperation(ctx *SetOperationContext) {}

// ExitSetOperation is called when production setOperation is exited.
func (s *BaseDorisParserListener) ExitSetOperation(ctx *SetOperationContext) {}

// EnterSetQuantifier is called when production setQuantifier is entered.
func (s *BaseDorisParserListener) EnterSetQuantifier(ctx *SetQuantifierContext) {}

// ExitSetQuantifier is called when production setQuantifier is exited.
func (s *BaseDorisParserListener) ExitSetQuantifier(ctx *SetQuantifierContext) {}

// EnterQueryPrimaryDefault is called when production queryPrimaryDefault is entered.
func (s *BaseDorisParserListener) EnterQueryPrimaryDefault(ctx *QueryPrimaryDefaultContext) {}

// ExitQueryPrimaryDefault is called when production queryPrimaryDefault is exited.
func (s *BaseDorisParserListener) ExitQueryPrimaryDefault(ctx *QueryPrimaryDefaultContext) {}

// EnterSubquery is called when production subquery is entered.
func (s *BaseDorisParserListener) EnterSubquery(ctx *SubqueryContext) {}

// ExitSubquery is called when production subquery is exited.
func (s *BaseDorisParserListener) ExitSubquery(ctx *SubqueryContext) {}

// EnterValuesTable is called when production valuesTable is entered.
func (s *BaseDorisParserListener) EnterValuesTable(ctx *ValuesTableContext) {}

// ExitValuesTable is called when production valuesTable is exited.
func (s *BaseDorisParserListener) ExitValuesTable(ctx *ValuesTableContext) {}

// EnterRegularQuerySpecification is called when production regularQuerySpecification is entered.
func (s *BaseDorisParserListener) EnterRegularQuerySpecification(ctx *RegularQuerySpecificationContext) {
}

// ExitRegularQuerySpecification is called when production regularQuerySpecification is exited.
func (s *BaseDorisParserListener) ExitRegularQuerySpecification(ctx *RegularQuerySpecificationContext) {
}

// EnterCte is called when production cte is entered.
func (s *BaseDorisParserListener) EnterCte(ctx *CteContext) {}

// ExitCte is called when production cte is exited.
func (s *BaseDorisParserListener) ExitCte(ctx *CteContext) {}

// EnterAliasQuery is called when production aliasQuery is entered.
func (s *BaseDorisParserListener) EnterAliasQuery(ctx *AliasQueryContext) {}

// ExitAliasQuery is called when production aliasQuery is exited.
func (s *BaseDorisParserListener) ExitAliasQuery(ctx *AliasQueryContext) {}

// EnterColumnAliases is called when production columnAliases is entered.
func (s *BaseDorisParserListener) EnterColumnAliases(ctx *ColumnAliasesContext) {}

// ExitColumnAliases is called when production columnAliases is exited.
func (s *BaseDorisParserListener) ExitColumnAliases(ctx *ColumnAliasesContext) {}

// EnterSelectClause is called when production selectClause is entered.
func (s *BaseDorisParserListener) EnterSelectClause(ctx *SelectClauseContext) {}

// ExitSelectClause is called when production selectClause is exited.
func (s *BaseDorisParserListener) ExitSelectClause(ctx *SelectClauseContext) {}

// EnterSelectColumnClause is called when production selectColumnClause is entered.
func (s *BaseDorisParserListener) EnterSelectColumnClause(ctx *SelectColumnClauseContext) {}

// ExitSelectColumnClause is called when production selectColumnClause is exited.
func (s *BaseDorisParserListener) ExitSelectColumnClause(ctx *SelectColumnClauseContext) {}

// EnterWhereClause is called when production whereClause is entered.
func (s *BaseDorisParserListener) EnterWhereClause(ctx *WhereClauseContext) {}

// ExitWhereClause is called when production whereClause is exited.
func (s *BaseDorisParserListener) ExitWhereClause(ctx *WhereClauseContext) {}

// EnterFromClause is called when production fromClause is entered.
func (s *BaseDorisParserListener) EnterFromClause(ctx *FromClauseContext) {}

// ExitFromClause is called when production fromClause is exited.
func (s *BaseDorisParserListener) ExitFromClause(ctx *FromClauseContext) {}

// EnterIntoClause is called when production intoClause is entered.
func (s *BaseDorisParserListener) EnterIntoClause(ctx *IntoClauseContext) {}

// ExitIntoClause is called when production intoClause is exited.
func (s *BaseDorisParserListener) ExitIntoClause(ctx *IntoClauseContext) {}

// EnterBulkCollectClause is called when production bulkCollectClause is entered.
func (s *BaseDorisParserListener) EnterBulkCollectClause(ctx *BulkCollectClauseContext) {}

// ExitBulkCollectClause is called when production bulkCollectClause is exited.
func (s *BaseDorisParserListener) ExitBulkCollectClause(ctx *BulkCollectClauseContext) {}

// EnterTableRow is called when production tableRow is entered.
func (s *BaseDorisParserListener) EnterTableRow(ctx *TableRowContext) {}

// ExitTableRow is called when production tableRow is exited.
func (s *BaseDorisParserListener) ExitTableRow(ctx *TableRowContext) {}

// EnterRelations is called when production relations is entered.
func (s *BaseDorisParserListener) EnterRelations(ctx *RelationsContext) {}

// ExitRelations is called when production relations is exited.
func (s *BaseDorisParserListener) ExitRelations(ctx *RelationsContext) {}

// EnterRelation is called when production relation is entered.
func (s *BaseDorisParserListener) EnterRelation(ctx *RelationContext) {}

// ExitRelation is called when production relation is exited.
func (s *BaseDorisParserListener) ExitRelation(ctx *RelationContext) {}

// EnterJoinRelation is called when production joinRelation is entered.
func (s *BaseDorisParserListener) EnterJoinRelation(ctx *JoinRelationContext) {}

// ExitJoinRelation is called when production joinRelation is exited.
func (s *BaseDorisParserListener) ExitJoinRelation(ctx *JoinRelationContext) {}

// EnterBracketDistributeType is called when production bracketDistributeType is entered.
func (s *BaseDorisParserListener) EnterBracketDistributeType(ctx *BracketDistributeTypeContext) {}

// ExitBracketDistributeType is called when production bracketDistributeType is exited.
func (s *BaseDorisParserListener) ExitBracketDistributeType(ctx *BracketDistributeTypeContext) {}

// EnterCommentDistributeType is called when production commentDistributeType is entered.
func (s *BaseDorisParserListener) EnterCommentDistributeType(ctx *CommentDistributeTypeContext) {}

// ExitCommentDistributeType is called when production commentDistributeType is exited.
func (s *BaseDorisParserListener) ExitCommentDistributeType(ctx *CommentDistributeTypeContext) {}

// EnterBracketRelationHint is called when production bracketRelationHint is entered.
func (s *BaseDorisParserListener) EnterBracketRelationHint(ctx *BracketRelationHintContext) {}

// ExitBracketRelationHint is called when production bracketRelationHint is exited.
func (s *BaseDorisParserListener) ExitBracketRelationHint(ctx *BracketRelationHintContext) {}

// EnterCommentRelationHint is called when production commentRelationHint is entered.
func (s *BaseDorisParserListener) EnterCommentRelationHint(ctx *CommentRelationHintContext) {}

// ExitCommentRelationHint is called when production commentRelationHint is exited.
func (s *BaseDorisParserListener) ExitCommentRelationHint(ctx *CommentRelationHintContext) {}

// EnterAggClause is called when production aggClause is entered.
func (s *BaseDorisParserListener) EnterAggClause(ctx *AggClauseContext) {}

// ExitAggClause is called when production aggClause is exited.
func (s *BaseDorisParserListener) ExitAggClause(ctx *AggClauseContext) {}

// EnterGroupingElement is called when production groupingElement is entered.
func (s *BaseDorisParserListener) EnterGroupingElement(ctx *GroupingElementContext) {}

// ExitGroupingElement is called when production groupingElement is exited.
func (s *BaseDorisParserListener) ExitGroupingElement(ctx *GroupingElementContext) {}

// EnterGroupingSet is called when production groupingSet is entered.
func (s *BaseDorisParserListener) EnterGroupingSet(ctx *GroupingSetContext) {}

// ExitGroupingSet is called when production groupingSet is exited.
func (s *BaseDorisParserListener) ExitGroupingSet(ctx *GroupingSetContext) {}

// EnterHavingClause is called when production havingClause is entered.
func (s *BaseDorisParserListener) EnterHavingClause(ctx *HavingClauseContext) {}

// ExitHavingClause is called when production havingClause is exited.
func (s *BaseDorisParserListener) ExitHavingClause(ctx *HavingClauseContext) {}

// EnterQualifyClause is called when production qualifyClause is entered.
func (s *BaseDorisParserListener) EnterQualifyClause(ctx *QualifyClauseContext) {}

// ExitQualifyClause is called when production qualifyClause is exited.
func (s *BaseDorisParserListener) ExitQualifyClause(ctx *QualifyClauseContext) {}

// EnterSelectHint is called when production selectHint is entered.
func (s *BaseDorisParserListener) EnterSelectHint(ctx *SelectHintContext) {}

// ExitSelectHint is called when production selectHint is exited.
func (s *BaseDorisParserListener) ExitSelectHint(ctx *SelectHintContext) {}

// EnterHintStatement is called when production hintStatement is entered.
func (s *BaseDorisParserListener) EnterHintStatement(ctx *HintStatementContext) {}

// ExitHintStatement is called when production hintStatement is exited.
func (s *BaseDorisParserListener) ExitHintStatement(ctx *HintStatementContext) {}

// EnterHintAssignment is called when production hintAssignment is entered.
func (s *BaseDorisParserListener) EnterHintAssignment(ctx *HintAssignmentContext) {}

// ExitHintAssignment is called when production hintAssignment is exited.
func (s *BaseDorisParserListener) ExitHintAssignment(ctx *HintAssignmentContext) {}

// EnterUpdateAssignment is called when production updateAssignment is entered.
func (s *BaseDorisParserListener) EnterUpdateAssignment(ctx *UpdateAssignmentContext) {}

// ExitUpdateAssignment is called when production updateAssignment is exited.
func (s *BaseDorisParserListener) ExitUpdateAssignment(ctx *UpdateAssignmentContext) {}

// EnterUpdateAssignmentSeq is called when production updateAssignmentSeq is entered.
func (s *BaseDorisParserListener) EnterUpdateAssignmentSeq(ctx *UpdateAssignmentSeqContext) {}

// ExitUpdateAssignmentSeq is called when production updateAssignmentSeq is exited.
func (s *BaseDorisParserListener) ExitUpdateAssignmentSeq(ctx *UpdateAssignmentSeqContext) {}

// EnterLateralView is called when production lateralView is entered.
func (s *BaseDorisParserListener) EnterLateralView(ctx *LateralViewContext) {}

// ExitLateralView is called when production lateralView is exited.
func (s *BaseDorisParserListener) ExitLateralView(ctx *LateralViewContext) {}

// EnterQueryOrganization is called when production queryOrganization is entered.
func (s *BaseDorisParserListener) EnterQueryOrganization(ctx *QueryOrganizationContext) {}

// ExitQueryOrganization is called when production queryOrganization is exited.
func (s *BaseDorisParserListener) ExitQueryOrganization(ctx *QueryOrganizationContext) {}

// EnterSortClause is called when production sortClause is entered.
func (s *BaseDorisParserListener) EnterSortClause(ctx *SortClauseContext) {}

// ExitSortClause is called when production sortClause is exited.
func (s *BaseDorisParserListener) ExitSortClause(ctx *SortClauseContext) {}

// EnterSortItem is called when production sortItem is entered.
func (s *BaseDorisParserListener) EnterSortItem(ctx *SortItemContext) {}

// ExitSortItem is called when production sortItem is exited.
func (s *BaseDorisParserListener) ExitSortItem(ctx *SortItemContext) {}

// EnterLimitClause is called when production limitClause is entered.
func (s *BaseDorisParserListener) EnterLimitClause(ctx *LimitClauseContext) {}

// ExitLimitClause is called when production limitClause is exited.
func (s *BaseDorisParserListener) ExitLimitClause(ctx *LimitClauseContext) {}

// EnterPartitionClause is called when production partitionClause is entered.
func (s *BaseDorisParserListener) EnterPartitionClause(ctx *PartitionClauseContext) {}

// ExitPartitionClause is called when production partitionClause is exited.
func (s *BaseDorisParserListener) ExitPartitionClause(ctx *PartitionClauseContext) {}

// EnterJoinType is called when production joinType is entered.
func (s *BaseDorisParserListener) EnterJoinType(ctx *JoinTypeContext) {}

// ExitJoinType is called when production joinType is exited.
func (s *BaseDorisParserListener) ExitJoinType(ctx *JoinTypeContext) {}

// EnterJoinCriteria is called when production joinCriteria is entered.
func (s *BaseDorisParserListener) EnterJoinCriteria(ctx *JoinCriteriaContext) {}

// ExitJoinCriteria is called when production joinCriteria is exited.
func (s *BaseDorisParserListener) ExitJoinCriteria(ctx *JoinCriteriaContext) {}

// EnterIdentifierList is called when production identifierList is entered.
func (s *BaseDorisParserListener) EnterIdentifierList(ctx *IdentifierListContext) {}

// ExitIdentifierList is called when production identifierList is exited.
func (s *BaseDorisParserListener) ExitIdentifierList(ctx *IdentifierListContext) {}

// EnterIdentifierSeq is called when production identifierSeq is entered.
func (s *BaseDorisParserListener) EnterIdentifierSeq(ctx *IdentifierSeqContext) {}

// ExitIdentifierSeq is called when production identifierSeq is exited.
func (s *BaseDorisParserListener) ExitIdentifierSeq(ctx *IdentifierSeqContext) {}

// EnterOptScanParams is called when production optScanParams is entered.
func (s *BaseDorisParserListener) EnterOptScanParams(ctx *OptScanParamsContext) {}

// ExitOptScanParams is called when production optScanParams is exited.
func (s *BaseDorisParserListener) ExitOptScanParams(ctx *OptScanParamsContext) {}

// EnterTableName is called when production tableName is entered.
func (s *BaseDorisParserListener) EnterTableName(ctx *TableNameContext) {}

// ExitTableName is called when production tableName is exited.
func (s *BaseDorisParserListener) ExitTableName(ctx *TableNameContext) {}

// EnterAliasedQuery is called when production aliasedQuery is entered.
func (s *BaseDorisParserListener) EnterAliasedQuery(ctx *AliasedQueryContext) {}

// ExitAliasedQuery is called when production aliasedQuery is exited.
func (s *BaseDorisParserListener) ExitAliasedQuery(ctx *AliasedQueryContext) {}

// EnterTableValuedFunction is called when production tableValuedFunction is entered.
func (s *BaseDorisParserListener) EnterTableValuedFunction(ctx *TableValuedFunctionContext) {}

// ExitTableValuedFunction is called when production tableValuedFunction is exited.
func (s *BaseDorisParserListener) ExitTableValuedFunction(ctx *TableValuedFunctionContext) {}

// EnterRelationList is called when production relationList is entered.
func (s *BaseDorisParserListener) EnterRelationList(ctx *RelationListContext) {}

// ExitRelationList is called when production relationList is exited.
func (s *BaseDorisParserListener) ExitRelationList(ctx *RelationListContext) {}

// EnterMaterializedViewName is called when production materializedViewName is entered.
func (s *BaseDorisParserListener) EnterMaterializedViewName(ctx *MaterializedViewNameContext) {}

// ExitMaterializedViewName is called when production materializedViewName is exited.
func (s *BaseDorisParserListener) ExitMaterializedViewName(ctx *MaterializedViewNameContext) {}

// EnterPropertyClause is called when production propertyClause is entered.
func (s *BaseDorisParserListener) EnterPropertyClause(ctx *PropertyClauseContext) {}

// ExitPropertyClause is called when production propertyClause is exited.
func (s *BaseDorisParserListener) ExitPropertyClause(ctx *PropertyClauseContext) {}

// EnterPropertyItemList is called when production propertyItemList is entered.
func (s *BaseDorisParserListener) EnterPropertyItemList(ctx *PropertyItemListContext) {}

// ExitPropertyItemList is called when production propertyItemList is exited.
func (s *BaseDorisParserListener) ExitPropertyItemList(ctx *PropertyItemListContext) {}

// EnterPropertyItem is called when production propertyItem is entered.
func (s *BaseDorisParserListener) EnterPropertyItem(ctx *PropertyItemContext) {}

// ExitPropertyItem is called when production propertyItem is exited.
func (s *BaseDorisParserListener) ExitPropertyItem(ctx *PropertyItemContext) {}

// EnterPropertyKey is called when production propertyKey is entered.
func (s *BaseDorisParserListener) EnterPropertyKey(ctx *PropertyKeyContext) {}

// ExitPropertyKey is called when production propertyKey is exited.
func (s *BaseDorisParserListener) ExitPropertyKey(ctx *PropertyKeyContext) {}

// EnterPropertyValue is called when production propertyValue is entered.
func (s *BaseDorisParserListener) EnterPropertyValue(ctx *PropertyValueContext) {}

// ExitPropertyValue is called when production propertyValue is exited.
func (s *BaseDorisParserListener) ExitPropertyValue(ctx *PropertyValueContext) {}

// EnterTableAlias is called when production tableAlias is entered.
func (s *BaseDorisParserListener) EnterTableAlias(ctx *TableAliasContext) {}

// ExitTableAlias is called when production tableAlias is exited.
func (s *BaseDorisParserListener) ExitTableAlias(ctx *TableAliasContext) {}

// EnterMultipartIdentifier is called when production multipartIdentifier is entered.
func (s *BaseDorisParserListener) EnterMultipartIdentifier(ctx *MultipartIdentifierContext) {}

// ExitMultipartIdentifier is called when production multipartIdentifier is exited.
func (s *BaseDorisParserListener) ExitMultipartIdentifier(ctx *MultipartIdentifierContext) {}

// EnterSimpleColumnDefs is called when production simpleColumnDefs is entered.
func (s *BaseDorisParserListener) EnterSimpleColumnDefs(ctx *SimpleColumnDefsContext) {}

// ExitSimpleColumnDefs is called when production simpleColumnDefs is exited.
func (s *BaseDorisParserListener) ExitSimpleColumnDefs(ctx *SimpleColumnDefsContext) {}

// EnterSimpleColumnDef is called when production simpleColumnDef is entered.
func (s *BaseDorisParserListener) EnterSimpleColumnDef(ctx *SimpleColumnDefContext) {}

// ExitSimpleColumnDef is called when production simpleColumnDef is exited.
func (s *BaseDorisParserListener) ExitSimpleColumnDef(ctx *SimpleColumnDefContext) {}

// EnterColumnDefs is called when production columnDefs is entered.
func (s *BaseDorisParserListener) EnterColumnDefs(ctx *ColumnDefsContext) {}

// ExitColumnDefs is called when production columnDefs is exited.
func (s *BaseDorisParserListener) ExitColumnDefs(ctx *ColumnDefsContext) {}

// EnterColumnDef is called when production columnDef is entered.
func (s *BaseDorisParserListener) EnterColumnDef(ctx *ColumnDefContext) {}

// ExitColumnDef is called when production columnDef is exited.
func (s *BaseDorisParserListener) ExitColumnDef(ctx *ColumnDefContext) {}

// EnterIndexDefs is called when production indexDefs is entered.
func (s *BaseDorisParserListener) EnterIndexDefs(ctx *IndexDefsContext) {}

// ExitIndexDefs is called when production indexDefs is exited.
func (s *BaseDorisParserListener) ExitIndexDefs(ctx *IndexDefsContext) {}

// EnterIndexDef is called when production indexDef is entered.
func (s *BaseDorisParserListener) EnterIndexDef(ctx *IndexDefContext) {}

// ExitIndexDef is called when production indexDef is exited.
func (s *BaseDorisParserListener) ExitIndexDef(ctx *IndexDefContext) {}

// EnterPartitionsDef is called when production partitionsDef is entered.
func (s *BaseDorisParserListener) EnterPartitionsDef(ctx *PartitionsDefContext) {}

// ExitPartitionsDef is called when production partitionsDef is exited.
func (s *BaseDorisParserListener) ExitPartitionsDef(ctx *PartitionsDefContext) {}

// EnterPartitionDef is called when production partitionDef is entered.
func (s *BaseDorisParserListener) EnterPartitionDef(ctx *PartitionDefContext) {}

// ExitPartitionDef is called when production partitionDef is exited.
func (s *BaseDorisParserListener) ExitPartitionDef(ctx *PartitionDefContext) {}

// EnterLessThanPartitionDef is called when production lessThanPartitionDef is entered.
func (s *BaseDorisParserListener) EnterLessThanPartitionDef(ctx *LessThanPartitionDefContext) {}

// ExitLessThanPartitionDef is called when production lessThanPartitionDef is exited.
func (s *BaseDorisParserListener) ExitLessThanPartitionDef(ctx *LessThanPartitionDefContext) {}

// EnterFixedPartitionDef is called when production fixedPartitionDef is entered.
func (s *BaseDorisParserListener) EnterFixedPartitionDef(ctx *FixedPartitionDefContext) {}

// ExitFixedPartitionDef is called when production fixedPartitionDef is exited.
func (s *BaseDorisParserListener) ExitFixedPartitionDef(ctx *FixedPartitionDefContext) {}

// EnterStepPartitionDef is called when production stepPartitionDef is entered.
func (s *BaseDorisParserListener) EnterStepPartitionDef(ctx *StepPartitionDefContext) {}

// ExitStepPartitionDef is called when production stepPartitionDef is exited.
func (s *BaseDorisParserListener) ExitStepPartitionDef(ctx *StepPartitionDefContext) {}

// EnterInPartitionDef is called when production inPartitionDef is entered.
func (s *BaseDorisParserListener) EnterInPartitionDef(ctx *InPartitionDefContext) {}

// ExitInPartitionDef is called when production inPartitionDef is exited.
func (s *BaseDorisParserListener) ExitInPartitionDef(ctx *InPartitionDefContext) {}

// EnterPartitionValueList is called when production partitionValueList is entered.
func (s *BaseDorisParserListener) EnterPartitionValueList(ctx *PartitionValueListContext) {}

// ExitPartitionValueList is called when production partitionValueList is exited.
func (s *BaseDorisParserListener) ExitPartitionValueList(ctx *PartitionValueListContext) {}

// EnterPartitionValueDef is called when production partitionValueDef is entered.
func (s *BaseDorisParserListener) EnterPartitionValueDef(ctx *PartitionValueDefContext) {}

// ExitPartitionValueDef is called when production partitionValueDef is exited.
func (s *BaseDorisParserListener) ExitPartitionValueDef(ctx *PartitionValueDefContext) {}

// EnterRollupDefs is called when production rollupDefs is entered.
func (s *BaseDorisParserListener) EnterRollupDefs(ctx *RollupDefsContext) {}

// ExitRollupDefs is called when production rollupDefs is exited.
func (s *BaseDorisParserListener) ExitRollupDefs(ctx *RollupDefsContext) {}

// EnterRollupDef is called when production rollupDef is entered.
func (s *BaseDorisParserListener) EnterRollupDef(ctx *RollupDefContext) {}

// ExitRollupDef is called when production rollupDef is exited.
func (s *BaseDorisParserListener) ExitRollupDef(ctx *RollupDefContext) {}

// EnterAggTypeDef is called when production aggTypeDef is entered.
func (s *BaseDorisParserListener) EnterAggTypeDef(ctx *AggTypeDefContext) {}

// ExitAggTypeDef is called when production aggTypeDef is exited.
func (s *BaseDorisParserListener) ExitAggTypeDef(ctx *AggTypeDefContext) {}

// EnterTabletList is called when production tabletList is entered.
func (s *BaseDorisParserListener) EnterTabletList(ctx *TabletListContext) {}

// ExitTabletList is called when production tabletList is exited.
func (s *BaseDorisParserListener) ExitTabletList(ctx *TabletListContext) {}

// EnterInlineTable is called when production inlineTable is entered.
func (s *BaseDorisParserListener) EnterInlineTable(ctx *InlineTableContext) {}

// ExitInlineTable is called when production inlineTable is exited.
func (s *BaseDorisParserListener) ExitInlineTable(ctx *InlineTableContext) {}

// EnterNamedExpression is called when production namedExpression is entered.
func (s *BaseDorisParserListener) EnterNamedExpression(ctx *NamedExpressionContext) {}

// ExitNamedExpression is called when production namedExpression is exited.
func (s *BaseDorisParserListener) ExitNamedExpression(ctx *NamedExpressionContext) {}

// EnterNamedExpressionSeq is called when production namedExpressionSeq is entered.
func (s *BaseDorisParserListener) EnterNamedExpressionSeq(ctx *NamedExpressionSeqContext) {}

// ExitNamedExpressionSeq is called when production namedExpressionSeq is exited.
func (s *BaseDorisParserListener) ExitNamedExpressionSeq(ctx *NamedExpressionSeqContext) {}

// EnterExpression is called when production expression is entered.
func (s *BaseDorisParserListener) EnterExpression(ctx *ExpressionContext) {}

// ExitExpression is called when production expression is exited.
func (s *BaseDorisParserListener) ExitExpression(ctx *ExpressionContext) {}

// EnterLambdaExpression is called when production lambdaExpression is entered.
func (s *BaseDorisParserListener) EnterLambdaExpression(ctx *LambdaExpressionContext) {}

// ExitLambdaExpression is called when production lambdaExpression is exited.
func (s *BaseDorisParserListener) ExitLambdaExpression(ctx *LambdaExpressionContext) {}

// EnterExist is called when production exist is entered.
func (s *BaseDorisParserListener) EnterExist(ctx *ExistContext) {}

// ExitExist is called when production exist is exited.
func (s *BaseDorisParserListener) ExitExist(ctx *ExistContext) {}

// EnterLogicalNot is called when production logicalNot is entered.
func (s *BaseDorisParserListener) EnterLogicalNot(ctx *LogicalNotContext) {}

// ExitLogicalNot is called when production logicalNot is exited.
func (s *BaseDorisParserListener) ExitLogicalNot(ctx *LogicalNotContext) {}

// EnterPredicated is called when production predicated is entered.
func (s *BaseDorisParserListener) EnterPredicated(ctx *PredicatedContext) {}

// ExitPredicated is called when production predicated is exited.
func (s *BaseDorisParserListener) ExitPredicated(ctx *PredicatedContext) {}

// EnterIsnull is called when production isnull is entered.
func (s *BaseDorisParserListener) EnterIsnull(ctx *IsnullContext) {}

// ExitIsnull is called when production isnull is exited.
func (s *BaseDorisParserListener) ExitIsnull(ctx *IsnullContext) {}

// EnterIs_not_null_pred is called when production is_not_null_pred is entered.
func (s *BaseDorisParserListener) EnterIs_not_null_pred(ctx *Is_not_null_predContext) {}

// ExitIs_not_null_pred is called when production is_not_null_pred is exited.
func (s *BaseDorisParserListener) ExitIs_not_null_pred(ctx *Is_not_null_predContext) {}

// EnterLogicalBinary is called when production logicalBinary is entered.
func (s *BaseDorisParserListener) EnterLogicalBinary(ctx *LogicalBinaryContext) {}

// ExitLogicalBinary is called when production logicalBinary is exited.
func (s *BaseDorisParserListener) ExitLogicalBinary(ctx *LogicalBinaryContext) {}

// EnterDoublePipes is called when production doublePipes is entered.
func (s *BaseDorisParserListener) EnterDoublePipes(ctx *DoublePipesContext) {}

// ExitDoublePipes is called when production doublePipes is exited.
func (s *BaseDorisParserListener) ExitDoublePipes(ctx *DoublePipesContext) {}

// EnterRowConstructor is called when production rowConstructor is entered.
func (s *BaseDorisParserListener) EnterRowConstructor(ctx *RowConstructorContext) {}

// ExitRowConstructor is called when production rowConstructor is exited.
func (s *BaseDorisParserListener) ExitRowConstructor(ctx *RowConstructorContext) {}

// EnterRowConstructorItem is called when production rowConstructorItem is entered.
func (s *BaseDorisParserListener) EnterRowConstructorItem(ctx *RowConstructorItemContext) {}

// ExitRowConstructorItem is called when production rowConstructorItem is exited.
func (s *BaseDorisParserListener) ExitRowConstructorItem(ctx *RowConstructorItemContext) {}

// EnterPredicate is called when production predicate is entered.
func (s *BaseDorisParserListener) EnterPredicate(ctx *PredicateContext) {}

// ExitPredicate is called when production predicate is exited.
func (s *BaseDorisParserListener) ExitPredicate(ctx *PredicateContext) {}

// EnterValueExpressionDefault is called when production valueExpressionDefault is entered.
func (s *BaseDorisParserListener) EnterValueExpressionDefault(ctx *ValueExpressionDefaultContext) {}

// ExitValueExpressionDefault is called when production valueExpressionDefault is exited.
func (s *BaseDorisParserListener) ExitValueExpressionDefault(ctx *ValueExpressionDefaultContext) {}

// EnterComparison is called when production comparison is entered.
func (s *BaseDorisParserListener) EnterComparison(ctx *ComparisonContext) {}

// ExitComparison is called when production comparison is exited.
func (s *BaseDorisParserListener) ExitComparison(ctx *ComparisonContext) {}

// EnterArithmeticBinary is called when production arithmeticBinary is entered.
func (s *BaseDorisParserListener) EnterArithmeticBinary(ctx *ArithmeticBinaryContext) {}

// ExitArithmeticBinary is called when production arithmeticBinary is exited.
func (s *BaseDorisParserListener) ExitArithmeticBinary(ctx *ArithmeticBinaryContext) {}

// EnterArithmeticUnary is called when production arithmeticUnary is entered.
func (s *BaseDorisParserListener) EnterArithmeticUnary(ctx *ArithmeticUnaryContext) {}

// ExitArithmeticUnary is called when production arithmeticUnary is exited.
func (s *BaseDorisParserListener) ExitArithmeticUnary(ctx *ArithmeticUnaryContext) {}

// EnterDereference is called when production dereference is entered.
func (s *BaseDorisParserListener) EnterDereference(ctx *DereferenceContext) {}

// ExitDereference is called when production dereference is exited.
func (s *BaseDorisParserListener) ExitDereference(ctx *DereferenceContext) {}

// EnterCurrentDate is called when production currentDate is entered.
func (s *BaseDorisParserListener) EnterCurrentDate(ctx *CurrentDateContext) {}

// ExitCurrentDate is called when production currentDate is exited.
func (s *BaseDorisParserListener) ExitCurrentDate(ctx *CurrentDateContext) {}

// EnterCast is called when production cast is entered.
func (s *BaseDorisParserListener) EnterCast(ctx *CastContext) {}

// ExitCast is called when production cast is exited.
func (s *BaseDorisParserListener) ExitCast(ctx *CastContext) {}

// EnterParenthesizedExpression is called when production parenthesizedExpression is entered.
func (s *BaseDorisParserListener) EnterParenthesizedExpression(ctx *ParenthesizedExpressionContext) {}

// ExitParenthesizedExpression is called when production parenthesizedExpression is exited.
func (s *BaseDorisParserListener) ExitParenthesizedExpression(ctx *ParenthesizedExpressionContext) {}

// EnterUserVariable is called when production userVariable is entered.
func (s *BaseDorisParserListener) EnterUserVariable(ctx *UserVariableContext) {}

// ExitUserVariable is called when production userVariable is exited.
func (s *BaseDorisParserListener) ExitUserVariable(ctx *UserVariableContext) {}

// EnterElementAt is called when production elementAt is entered.
func (s *BaseDorisParserListener) EnterElementAt(ctx *ElementAtContext) {}

// ExitElementAt is called when production elementAt is exited.
func (s *BaseDorisParserListener) ExitElementAt(ctx *ElementAtContext) {}

// EnterLocalTimestamp is called when production localTimestamp is entered.
func (s *BaseDorisParserListener) EnterLocalTimestamp(ctx *LocalTimestampContext) {}

// ExitLocalTimestamp is called when production localTimestamp is exited.
func (s *BaseDorisParserListener) ExitLocalTimestamp(ctx *LocalTimestampContext) {}

// EnterCharFunction is called when production charFunction is entered.
func (s *BaseDorisParserListener) EnterCharFunction(ctx *CharFunctionContext) {}

// ExitCharFunction is called when production charFunction is exited.
func (s *BaseDorisParserListener) ExitCharFunction(ctx *CharFunctionContext) {}

// EnterIntervalLiteral is called when production intervalLiteral is entered.
func (s *BaseDorisParserListener) EnterIntervalLiteral(ctx *IntervalLiteralContext) {}

// ExitIntervalLiteral is called when production intervalLiteral is exited.
func (s *BaseDorisParserListener) ExitIntervalLiteral(ctx *IntervalLiteralContext) {}

// EnterSimpleCase is called when production simpleCase is entered.
func (s *BaseDorisParserListener) EnterSimpleCase(ctx *SimpleCaseContext) {}

// ExitSimpleCase is called when production simpleCase is exited.
func (s *BaseDorisParserListener) ExitSimpleCase(ctx *SimpleCaseContext) {}

// EnterColumnReference is called when production columnReference is entered.
func (s *BaseDorisParserListener) EnterColumnReference(ctx *ColumnReferenceContext) {}

// ExitColumnReference is called when production columnReference is exited.
func (s *BaseDorisParserListener) ExitColumnReference(ctx *ColumnReferenceContext) {}

// EnterStar is called when production star is entered.
func (s *BaseDorisParserListener) EnterStar(ctx *StarContext) {}

// ExitStar is called when production star is exited.
func (s *BaseDorisParserListener) ExitStar(ctx *StarContext) {}

// EnterSessionUser is called when production sessionUser is entered.
func (s *BaseDorisParserListener) EnterSessionUser(ctx *SessionUserContext) {}

// ExitSessionUser is called when production sessionUser is exited.
func (s *BaseDorisParserListener) ExitSessionUser(ctx *SessionUserContext) {}

// EnterConvertType is called when production convertType is entered.
func (s *BaseDorisParserListener) EnterConvertType(ctx *ConvertTypeContext) {}

// ExitConvertType is called when production convertType is exited.
func (s *BaseDorisParserListener) ExitConvertType(ctx *ConvertTypeContext) {}

// EnterConvertCharSet is called when production convertCharSet is entered.
func (s *BaseDorisParserListener) EnterConvertCharSet(ctx *ConvertCharSetContext) {}

// ExitConvertCharSet is called when production convertCharSet is exited.
func (s *BaseDorisParserListener) ExitConvertCharSet(ctx *ConvertCharSetContext) {}

// EnterSubqueryExpression is called when production subqueryExpression is entered.
func (s *BaseDorisParserListener) EnterSubqueryExpression(ctx *SubqueryExpressionContext) {}

// ExitSubqueryExpression is called when production subqueryExpression is exited.
func (s *BaseDorisParserListener) ExitSubqueryExpression(ctx *SubqueryExpressionContext) {}

// EnterEncryptKey is called when production encryptKey is entered.
func (s *BaseDorisParserListener) EnterEncryptKey(ctx *EncryptKeyContext) {}

// ExitEncryptKey is called when production encryptKey is exited.
func (s *BaseDorisParserListener) ExitEncryptKey(ctx *EncryptKeyContext) {}

// EnterCurrentTime is called when production currentTime is entered.
func (s *BaseDorisParserListener) EnterCurrentTime(ctx *CurrentTimeContext) {}

// ExitCurrentTime is called when production currentTime is exited.
func (s *BaseDorisParserListener) ExitCurrentTime(ctx *CurrentTimeContext) {}

// EnterLocalTime is called when production localTime is entered.
func (s *BaseDorisParserListener) EnterLocalTime(ctx *LocalTimeContext) {}

// ExitLocalTime is called when production localTime is exited.
func (s *BaseDorisParserListener) ExitLocalTime(ctx *LocalTimeContext) {}

// EnterSystemVariable is called when production systemVariable is entered.
func (s *BaseDorisParserListener) EnterSystemVariable(ctx *SystemVariableContext) {}

// ExitSystemVariable is called when production systemVariable is exited.
func (s *BaseDorisParserListener) ExitSystemVariable(ctx *SystemVariableContext) {}

// EnterCollate is called when production collate is entered.
func (s *BaseDorisParserListener) EnterCollate(ctx *CollateContext) {}

// ExitCollate is called when production collate is exited.
func (s *BaseDorisParserListener) ExitCollate(ctx *CollateContext) {}

// EnterCurrentUser is called when production currentUser is entered.
func (s *BaseDorisParserListener) EnterCurrentUser(ctx *CurrentUserContext) {}

// ExitCurrentUser is called when production currentUser is exited.
func (s *BaseDorisParserListener) ExitCurrentUser(ctx *CurrentUserContext) {}

// EnterConstantDefault is called when production constantDefault is entered.
func (s *BaseDorisParserListener) EnterConstantDefault(ctx *ConstantDefaultContext) {}

// ExitConstantDefault is called when production constantDefault is exited.
func (s *BaseDorisParserListener) ExitConstantDefault(ctx *ConstantDefaultContext) {}

// EnterExtract is called when production extract is entered.
func (s *BaseDorisParserListener) EnterExtract(ctx *ExtractContext) {}

// ExitExtract is called when production extract is exited.
func (s *BaseDorisParserListener) ExitExtract(ctx *ExtractContext) {}

// EnterCurrentTimestamp is called when production currentTimestamp is entered.
func (s *BaseDorisParserListener) EnterCurrentTimestamp(ctx *CurrentTimestampContext) {}

// ExitCurrentTimestamp is called when production currentTimestamp is exited.
func (s *BaseDorisParserListener) ExitCurrentTimestamp(ctx *CurrentTimestampContext) {}

// EnterFunctionCall is called when production functionCall is entered.
func (s *BaseDorisParserListener) EnterFunctionCall(ctx *FunctionCallContext) {}

// ExitFunctionCall is called when production functionCall is exited.
func (s *BaseDorisParserListener) ExitFunctionCall(ctx *FunctionCallContext) {}

// EnterArraySlice is called when production arraySlice is entered.
func (s *BaseDorisParserListener) EnterArraySlice(ctx *ArraySliceContext) {}

// ExitArraySlice is called when production arraySlice is exited.
func (s *BaseDorisParserListener) ExitArraySlice(ctx *ArraySliceContext) {}

// EnterSearchedCase is called when production searchedCase is entered.
func (s *BaseDorisParserListener) EnterSearchedCase(ctx *SearchedCaseContext) {}

// ExitSearchedCase is called when production searchedCase is exited.
func (s *BaseDorisParserListener) ExitSearchedCase(ctx *SearchedCaseContext) {}

// EnterExcept is called when production except is entered.
func (s *BaseDorisParserListener) EnterExcept(ctx *ExceptContext) {}

// ExitExcept is called when production except is exited.
func (s *BaseDorisParserListener) ExitExcept(ctx *ExceptContext) {}

// EnterReplace is called when production replace is entered.
func (s *BaseDorisParserListener) EnterReplace(ctx *ReplaceContext) {}

// ExitReplace is called when production replace is exited.
func (s *BaseDorisParserListener) ExitReplace(ctx *ReplaceContext) {}

// EnterCastDataType is called when production castDataType is entered.
func (s *BaseDorisParserListener) EnterCastDataType(ctx *CastDataTypeContext) {}

// ExitCastDataType is called when production castDataType is exited.
func (s *BaseDorisParserListener) ExitCastDataType(ctx *CastDataTypeContext) {}

// EnterFunctionCallExpression is called when production functionCallExpression is entered.
func (s *BaseDorisParserListener) EnterFunctionCallExpression(ctx *FunctionCallExpressionContext) {}

// ExitFunctionCallExpression is called when production functionCallExpression is exited.
func (s *BaseDorisParserListener) ExitFunctionCallExpression(ctx *FunctionCallExpressionContext) {}

// EnterFunctionIdentifier is called when production functionIdentifier is entered.
func (s *BaseDorisParserListener) EnterFunctionIdentifier(ctx *FunctionIdentifierContext) {}

// ExitFunctionIdentifier is called when production functionIdentifier is exited.
func (s *BaseDorisParserListener) ExitFunctionIdentifier(ctx *FunctionIdentifierContext) {}

// EnterFunctionNameIdentifier is called when production functionNameIdentifier is entered.
func (s *BaseDorisParserListener) EnterFunctionNameIdentifier(ctx *FunctionNameIdentifierContext) {}

// ExitFunctionNameIdentifier is called when production functionNameIdentifier is exited.
func (s *BaseDorisParserListener) ExitFunctionNameIdentifier(ctx *FunctionNameIdentifierContext) {}

// EnterWindowSpec is called when production windowSpec is entered.
func (s *BaseDorisParserListener) EnterWindowSpec(ctx *WindowSpecContext) {}

// ExitWindowSpec is called when production windowSpec is exited.
func (s *BaseDorisParserListener) ExitWindowSpec(ctx *WindowSpecContext) {}

// EnterWindowFrame is called when production windowFrame is entered.
func (s *BaseDorisParserListener) EnterWindowFrame(ctx *WindowFrameContext) {}

// ExitWindowFrame is called when production windowFrame is exited.
func (s *BaseDorisParserListener) ExitWindowFrame(ctx *WindowFrameContext) {}

// EnterFrameUnits is called when production frameUnits is entered.
func (s *BaseDorisParserListener) EnterFrameUnits(ctx *FrameUnitsContext) {}

// ExitFrameUnits is called when production frameUnits is exited.
func (s *BaseDorisParserListener) ExitFrameUnits(ctx *FrameUnitsContext) {}

// EnterFrameBoundary is called when production frameBoundary is entered.
func (s *BaseDorisParserListener) EnterFrameBoundary(ctx *FrameBoundaryContext) {}

// ExitFrameBoundary is called when production frameBoundary is exited.
func (s *BaseDorisParserListener) ExitFrameBoundary(ctx *FrameBoundaryContext) {}

// EnterQualifiedName is called when production qualifiedName is entered.
func (s *BaseDorisParserListener) EnterQualifiedName(ctx *QualifiedNameContext) {}

// ExitQualifiedName is called when production qualifiedName is exited.
func (s *BaseDorisParserListener) ExitQualifiedName(ctx *QualifiedNameContext) {}

// EnterSpecifiedPartition is called when production specifiedPartition is entered.
func (s *BaseDorisParserListener) EnterSpecifiedPartition(ctx *SpecifiedPartitionContext) {}

// ExitSpecifiedPartition is called when production specifiedPartition is exited.
func (s *BaseDorisParserListener) ExitSpecifiedPartition(ctx *SpecifiedPartitionContext) {}

// EnterNullLiteral is called when production nullLiteral is entered.
func (s *BaseDorisParserListener) EnterNullLiteral(ctx *NullLiteralContext) {}

// ExitNullLiteral is called when production nullLiteral is exited.
func (s *BaseDorisParserListener) ExitNullLiteral(ctx *NullLiteralContext) {}

// EnterTypeConstructor is called when production typeConstructor is entered.
func (s *BaseDorisParserListener) EnterTypeConstructor(ctx *TypeConstructorContext) {}

// ExitTypeConstructor is called when production typeConstructor is exited.
func (s *BaseDorisParserListener) ExitTypeConstructor(ctx *TypeConstructorContext) {}

// EnterNumericLiteral is called when production numericLiteral is entered.
func (s *BaseDorisParserListener) EnterNumericLiteral(ctx *NumericLiteralContext) {}

// ExitNumericLiteral is called when production numericLiteral is exited.
func (s *BaseDorisParserListener) ExitNumericLiteral(ctx *NumericLiteralContext) {}

// EnterBooleanLiteral is called when production booleanLiteral is entered.
func (s *BaseDorisParserListener) EnterBooleanLiteral(ctx *BooleanLiteralContext) {}

// ExitBooleanLiteral is called when production booleanLiteral is exited.
func (s *BaseDorisParserListener) ExitBooleanLiteral(ctx *BooleanLiteralContext) {}

// EnterStringLiteral is called when production stringLiteral is entered.
func (s *BaseDorisParserListener) EnterStringLiteral(ctx *StringLiteralContext) {}

// ExitStringLiteral is called when production stringLiteral is exited.
func (s *BaseDorisParserListener) ExitStringLiteral(ctx *StringLiteralContext) {}

// EnterArrayLiteral is called when production arrayLiteral is entered.
func (s *BaseDorisParserListener) EnterArrayLiteral(ctx *ArrayLiteralContext) {}

// ExitArrayLiteral is called when production arrayLiteral is exited.
func (s *BaseDorisParserListener) ExitArrayLiteral(ctx *ArrayLiteralContext) {}

// EnterMapLiteral is called when production mapLiteral is entered.
func (s *BaseDorisParserListener) EnterMapLiteral(ctx *MapLiteralContext) {}

// ExitMapLiteral is called when production mapLiteral is exited.
func (s *BaseDorisParserListener) ExitMapLiteral(ctx *MapLiteralContext) {}

// EnterStructLiteral is called when production structLiteral is entered.
func (s *BaseDorisParserListener) EnterStructLiteral(ctx *StructLiteralContext) {}

// ExitStructLiteral is called when production structLiteral is exited.
func (s *BaseDorisParserListener) ExitStructLiteral(ctx *StructLiteralContext) {}

// EnterPlaceholder is called when production placeholder is entered.
func (s *BaseDorisParserListener) EnterPlaceholder(ctx *PlaceholderContext) {}

// ExitPlaceholder is called when production placeholder is exited.
func (s *BaseDorisParserListener) ExitPlaceholder(ctx *PlaceholderContext) {}

// EnterComparisonOperator is called when production comparisonOperator is entered.
func (s *BaseDorisParserListener) EnterComparisonOperator(ctx *ComparisonOperatorContext) {}

// ExitComparisonOperator is called when production comparisonOperator is exited.
func (s *BaseDorisParserListener) ExitComparisonOperator(ctx *ComparisonOperatorContext) {}

// EnterBooleanValue is called when production booleanValue is entered.
func (s *BaseDorisParserListener) EnterBooleanValue(ctx *BooleanValueContext) {}

// ExitBooleanValue is called when production booleanValue is exited.
func (s *BaseDorisParserListener) ExitBooleanValue(ctx *BooleanValueContext) {}

// EnterWhenClause is called when production whenClause is entered.
func (s *BaseDorisParserListener) EnterWhenClause(ctx *WhenClauseContext) {}

// ExitWhenClause is called when production whenClause is exited.
func (s *BaseDorisParserListener) ExitWhenClause(ctx *WhenClauseContext) {}

// EnterInterval is called when production interval is entered.
func (s *BaseDorisParserListener) EnterInterval(ctx *IntervalContext) {}

// ExitInterval is called when production interval is exited.
func (s *BaseDorisParserListener) ExitInterval(ctx *IntervalContext) {}

// EnterUnitIdentifier is called when production unitIdentifier is entered.
func (s *BaseDorisParserListener) EnterUnitIdentifier(ctx *UnitIdentifierContext) {}

// ExitUnitIdentifier is called when production unitIdentifier is exited.
func (s *BaseDorisParserListener) ExitUnitIdentifier(ctx *UnitIdentifierContext) {}

// EnterDataTypeWithNullable is called when production dataTypeWithNullable is entered.
func (s *BaseDorisParserListener) EnterDataTypeWithNullable(ctx *DataTypeWithNullableContext) {}

// ExitDataTypeWithNullable is called when production dataTypeWithNullable is exited.
func (s *BaseDorisParserListener) ExitDataTypeWithNullable(ctx *DataTypeWithNullableContext) {}

// EnterComplexDataType is called when production complexDataType is entered.
func (s *BaseDorisParserListener) EnterComplexDataType(ctx *ComplexDataTypeContext) {}

// ExitComplexDataType is called when production complexDataType is exited.
func (s *BaseDorisParserListener) ExitComplexDataType(ctx *ComplexDataTypeContext) {}

// EnterAggStateDataType is called when production aggStateDataType is entered.
func (s *BaseDorisParserListener) EnterAggStateDataType(ctx *AggStateDataTypeContext) {}

// ExitAggStateDataType is called when production aggStateDataType is exited.
func (s *BaseDorisParserListener) ExitAggStateDataType(ctx *AggStateDataTypeContext) {}

// EnterPrimitiveDataType is called when production primitiveDataType is entered.
func (s *BaseDorisParserListener) EnterPrimitiveDataType(ctx *PrimitiveDataTypeContext) {}

// ExitPrimitiveDataType is called when production primitiveDataType is exited.
func (s *BaseDorisParserListener) ExitPrimitiveDataType(ctx *PrimitiveDataTypeContext) {}

// EnterPrimitiveColType is called when production primitiveColType is entered.
func (s *BaseDorisParserListener) EnterPrimitiveColType(ctx *PrimitiveColTypeContext) {}

// ExitPrimitiveColType is called when production primitiveColType is exited.
func (s *BaseDorisParserListener) ExitPrimitiveColType(ctx *PrimitiveColTypeContext) {}

// EnterComplexColTypeList is called when production complexColTypeList is entered.
func (s *BaseDorisParserListener) EnterComplexColTypeList(ctx *ComplexColTypeListContext) {}

// ExitComplexColTypeList is called when production complexColTypeList is exited.
func (s *BaseDorisParserListener) ExitComplexColTypeList(ctx *ComplexColTypeListContext) {}

// EnterComplexColType is called when production complexColType is entered.
func (s *BaseDorisParserListener) EnterComplexColType(ctx *ComplexColTypeContext) {}

// ExitComplexColType is called when production complexColType is exited.
func (s *BaseDorisParserListener) ExitComplexColType(ctx *ComplexColTypeContext) {}

// EnterCommentSpec is called when production commentSpec is entered.
func (s *BaseDorisParserListener) EnterCommentSpec(ctx *CommentSpecContext) {}

// ExitCommentSpec is called when production commentSpec is exited.
func (s *BaseDorisParserListener) ExitCommentSpec(ctx *CommentSpecContext) {}

// EnterSample is called when production sample is entered.
func (s *BaseDorisParserListener) EnterSample(ctx *SampleContext) {}

// ExitSample is called when production sample is exited.
func (s *BaseDorisParserListener) ExitSample(ctx *SampleContext) {}

// EnterSampleByPercentile is called when production sampleByPercentile is entered.
func (s *BaseDorisParserListener) EnterSampleByPercentile(ctx *SampleByPercentileContext) {}

// ExitSampleByPercentile is called when production sampleByPercentile is exited.
func (s *BaseDorisParserListener) ExitSampleByPercentile(ctx *SampleByPercentileContext) {}

// EnterSampleByRows is called when production sampleByRows is entered.
func (s *BaseDorisParserListener) EnterSampleByRows(ctx *SampleByRowsContext) {}

// ExitSampleByRows is called when production sampleByRows is exited.
func (s *BaseDorisParserListener) ExitSampleByRows(ctx *SampleByRowsContext) {}

// EnterTableSnapshot is called when production tableSnapshot is entered.
func (s *BaseDorisParserListener) EnterTableSnapshot(ctx *TableSnapshotContext) {}

// ExitTableSnapshot is called when production tableSnapshot is exited.
func (s *BaseDorisParserListener) ExitTableSnapshot(ctx *TableSnapshotContext) {}

// EnterErrorCapturingIdentifier is called when production errorCapturingIdentifier is entered.
func (s *BaseDorisParserListener) EnterErrorCapturingIdentifier(ctx *ErrorCapturingIdentifierContext) {
}

// ExitErrorCapturingIdentifier is called when production errorCapturingIdentifier is exited.
func (s *BaseDorisParserListener) ExitErrorCapturingIdentifier(ctx *ErrorCapturingIdentifierContext) {
}

// EnterErrorIdent is called when production errorIdent is entered.
func (s *BaseDorisParserListener) EnterErrorIdent(ctx *ErrorIdentContext) {}

// ExitErrorIdent is called when production errorIdent is exited.
func (s *BaseDorisParserListener) ExitErrorIdent(ctx *ErrorIdentContext) {}

// EnterRealIdent is called when production realIdent is entered.
func (s *BaseDorisParserListener) EnterRealIdent(ctx *RealIdentContext) {}

// ExitRealIdent is called when production realIdent is exited.
func (s *BaseDorisParserListener) ExitRealIdent(ctx *RealIdentContext) {}

// EnterIdentifier is called when production identifier is entered.
func (s *BaseDorisParserListener) EnterIdentifier(ctx *IdentifierContext) {}

// ExitIdentifier is called when production identifier is exited.
func (s *BaseDorisParserListener) ExitIdentifier(ctx *IdentifierContext) {}

// EnterUnquotedIdentifier is called when production unquotedIdentifier is entered.
func (s *BaseDorisParserListener) EnterUnquotedIdentifier(ctx *UnquotedIdentifierContext) {}

// ExitUnquotedIdentifier is called when production unquotedIdentifier is exited.
func (s *BaseDorisParserListener) ExitUnquotedIdentifier(ctx *UnquotedIdentifierContext) {}

// EnterQuotedIdentifierAlternative is called when production quotedIdentifierAlternative is entered.
func (s *BaseDorisParserListener) EnterQuotedIdentifierAlternative(ctx *QuotedIdentifierAlternativeContext) {
}

// ExitQuotedIdentifierAlternative is called when production quotedIdentifierAlternative is exited.
func (s *BaseDorisParserListener) ExitQuotedIdentifierAlternative(ctx *QuotedIdentifierAlternativeContext) {
}

// EnterQuotedIdentifier is called when production quotedIdentifier is entered.
func (s *BaseDorisParserListener) EnterQuotedIdentifier(ctx *QuotedIdentifierContext) {}

// ExitQuotedIdentifier is called when production quotedIdentifier is exited.
func (s *BaseDorisParserListener) ExitQuotedIdentifier(ctx *QuotedIdentifierContext) {}

// EnterIntegerLiteral is called when production integerLiteral is entered.
func (s *BaseDorisParserListener) EnterIntegerLiteral(ctx *IntegerLiteralContext) {}

// ExitIntegerLiteral is called when production integerLiteral is exited.
func (s *BaseDorisParserListener) ExitIntegerLiteral(ctx *IntegerLiteralContext) {}

// EnterDecimalLiteral is called when production decimalLiteral is entered.
func (s *BaseDorisParserListener) EnterDecimalLiteral(ctx *DecimalLiteralContext) {}

// ExitDecimalLiteral is called when production decimalLiteral is exited.
func (s *BaseDorisParserListener) ExitDecimalLiteral(ctx *DecimalLiteralContext) {}

// EnterNonReserved is called when production nonReserved is entered.
func (s *BaseDorisParserListener) EnterNonReserved(ctx *NonReservedContext) {}

// ExitNonReserved is called when production nonReserved is exited.
func (s *BaseDorisParserListener) ExitNonReserved(ctx *NonReservedContext) {}
