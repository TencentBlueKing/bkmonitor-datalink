// Tencent is pleased to support the open source community by making
// 蓝鲸智云 - 监控平台 (BlueKing - Monitor) available.
// Copyright (C) 2022 THL A29 Limited, a Tencent company. All rights reserved.
// Licensed under the MIT License (the "License"); you may not use this file except in compliance with the License.
// You may obtain a copy of the License at http://opensource.org/licenses/MIT
// Unless required by applicable law or agreed to in writing, software distributed under the License is distributed on
// an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package errors

import (
	"github.com/pkg/errors"
)

var (
	// Wrapf returns an error annotating err with a stack trace
	// at the point Wrapf is called, and the format specifier.
	// If err is nil, Wrapf returns nil.
	Wrapf = errors.Wrapf

	// Wrap returns an error annotating err with a stack trace
	// at the point Wrap is called, and the supplied message.
	// If err is nil, Wrap returns nil.
	Wrap = errors.Wrap

	// New returns an error with the supplied message.
	// New also records the stack trace at the point it was called.
	New = errors.New

	// Errorf formats according to a format specifier and returns the string
	// as a value that satisfies error.
	// Errorf also records the stack trace at the point it was called.
	Errorf = errors.Errorf

	// Cause returns the underlying cause of the error, if possible.
	// An error value has a cause if it implements the following
	// interface:
	//
	//     type causer interface {
	//            Cause() error
	//     }
	//
	// If the error does not implement Cause, the original error will
	// be returned. If the error is nil, nil will be returned without further
	// investigation.
	Cause = errors.Cause

	// WithStack annotates err with a stack trace at the point WithStack was called.
	// If err is nil, WithStack returns nil.
	WithStack = errors.WithStack

	// WithMessagef annotates err with the format specifier.
	// If err is nil, WithMessagef returns nil.
	WithMessagef = errors.WithMessagef

	// WithMessage annotates err with a new message.
	// If err is nil, WithMessage returns nil.
	WithMessage = errors.WithMessage
)
