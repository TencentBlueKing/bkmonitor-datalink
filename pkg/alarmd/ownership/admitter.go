// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing Monitor) available.
// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.

package ownership

import (
	"context"
	"errors"
	"time"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/contract"
	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/alarmd/execution"
)

type FenceChecker interface {
	CheckFence(context.Context, execution.OwnerFence, time.Time) error
}

type ActivationReader interface {
	IsPlanActive(
		context.Context,
		execution.FrozenExecutionContractRef,
		execution.PlanIdentity,
		execution.StateApplyEpoch,
	) (bool, error)
}

type Admitter struct {
	fences     FenceChecker
	activation ActivationReader
	now        func() time.Time
}

func NewAdmitter(fences FenceChecker, activation ActivationReader, now func() time.Time) (*Admitter, error) {
	if fences == nil || activation == nil || now == nil {
		return nil, errors.New("alarmd ownership: fence, activation and clock are required")
	}
	return &Admitter{fences: fences, activation: activation, now: now}, nil
}

func (admitter *Admitter) Check(
	ctx context.Context,
	request execution.SideEffectAdmissionRequest,
) (execution.SideEffectAdmissionResult, error) {
	if admitter == nil {
		return execution.SideEffectAdmissionResult{}, errors.New("alarmd ownership: initialized admitter is required")
	}
	if err := request.Contract.Validate(); err != nil {
		return execution.SideEffectAdmissionResult{}, err
	}
	if err := request.OwnerFence.Validate(request.Contract); err != nil {
		return execution.SideEffectAdmissionResult{}, err
	}
	if request.Plan.TenantID == "" || request.Plan.BusinessID == "" || request.Plan.StrategyID == "" || request.StateApplyEpoch == 0 {
		return execution.SideEffectAdmissionResult{}, errors.New("alarmd ownership: incomplete Plan admission identity")
	}
	if err := admitter.fences.CheckFence(ctx, request.OwnerFence, admitter.now()); err != nil {
		return execution.SideEffectAdmissionResult{}, err
	}
	active, err := admitter.activation.IsPlanActive(
		ctx, request.Contract, request.Plan, request.StateApplyEpoch,
	)
	if err != nil {
		return execution.SideEffectAdmissionResult{}, err
	}
	if !active {
		return execution.SideEffectAdmissionResult{
			Admitted: false, ReasonCode: execution.ReasonCode(contract.ReasonConfigDrift),
		}, nil
	}
	return execution.SideEffectAdmissionResult{Admitted: true}, nil
}
