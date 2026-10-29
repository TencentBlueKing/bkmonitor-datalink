// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package rollout

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"unicode"

	"k8s.io/apimachinery/pkg/util/validation"
)

// The codes a refused or failed image change is named by.
const (
	CodeInvalid       = "invalid_input"
	CodeTargetChanged = "target_changed"
	CodeAPIError      = "api_error"
	CodeStore         = "receipt_store_unavailable"
	CodeNotFound      = "receipt_not_found"
	CodeForbidden     = "forbidden"
)

// Error names why an image change was refused or failed.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// MaxImageBytes bounds an image reference.
const MaxImageBytes = 512

// Request asks for one container of one Deployment to run another image.
// UID is the Deployment's as the caller discovered it.
type Request struct {
	Namespace, Name, UID, Container, Image string
	SessionID, RequestID                   string
	Operator, IssuedVia                    string
}

// Receipt is what an image change answers at once: its receipt id, the
// image it replaced, the state it started in and any warnings. The rollout
// is watched afterwards and read by the id.
type Receipt struct {
	Record
	Applied bool `json:"applied"`
}

// Warnings an image change carries without being refused: the owner may
// mean it, and only they can say.
const (
	WarnPaused       = "the deployment is paused: the rollout will not start, and a paused rollout is never reverted"
	WarnNoOldServing = "the rollout may stop every old Pod before a new one is ready (Recreate, or maxUnavailable covering every replica): " +
		"if this deployment is the one serving this change and the new image never becomes ready, no process is left to revert it"
)

// Start changes the image and hands the rollout to the driver. The receipt
// is written before the Deployment is: without it, nobody could revert the
// change if this process stopped. A store that cannot take it refuses the
// change. The image write tests the image read, so a change made between
// the read and the write is refused rather than overwritten.
func (d *Driver) Start(ctx context.Context, request Request) (Receipt, error) {
	if err := validRequest(request); err != nil {
		return Receipt{}, err
	}
	dep, err := d.getDeployment(ctx, request.Namespace, request.Name)
	if errors.Is(err, ErrDeploymentGone) {
		return Receipt{}, &Error{Code: CodeTargetChanged, Message: "the deployment by this name does not exist"}
	}
	if errors.Is(err, ErrForbidden) {
		return Receipt{}, &Error{Code: CodeForbidden, Message: "the apiserver refused this deployment to the channel's service account"}
	}
	if err != nil {
		return Receipt{}, &Error{Code: CodeAPIError, Message: "the deployment could not be read"}
	}
	if dep.UID != request.UID {
		return Receipt{}, &Error{Code: CodeTargetChanged, Message: "the deployment by this name is not the one discovered"}
	}
	previous, index := dep.image(request.Container)
	if index < 0 {
		return Receipt{}, &Error{Code: CodeInvalid, Message: "the deployment has no container by this name"}
	}
	id, err := receiptID()
	if err != nil {
		return Receipt{}, &Error{Code: CodeStore, Message: "a receipt id could not be made"}
	}
	now := d.now()
	record := Record{ID: id, Namespace: request.Namespace, Name: request.Name, UID: request.UID, Container: request.Container,
		Previous: previous, Image: request.Image, State: StatePending, Warnings: warnings(dep),
		SessionID: request.SessionID, RequestID: request.RequestID, Operator: request.Operator, IssuedVia: request.IssuedVia, StartedAt: now.UnixMilli()}
	if previous == request.Image {
		record = end(record, StateUnchanged, "the container already runs this image", now)
		return Receipt{Record: record}, nil
	}
	if err := d.store.Create(ctx, record, d.owner); err != nil {
		return Receipt{}, &Error{Code: CodeStore, Message: "the receipt could not be stored, so the image was not changed"}
	}
	generation, err := d.setImage(ctx, record, index, previous, request.Image)
	if err != nil {
		state, code, message := StateNotApplied, CodeAPIError, "the image write failed"
		if errors.Is(err, ErrImageChanged) {
			state, code, message = StateSuperseded, CodeTargetChanged, "the deployment or its image changed before the write"
		}
		if errors.Is(err, ErrDeploymentGone) {
			state, code, message = StateTargetChanged, CodeTargetChanged, "the deployment was deleted before the write"
		}
		if errors.Is(err, ErrForbidden) {
			code, message = CodeForbidden, "the apiserver refused the image write to the channel's service account"
		}
		record = end(record, state, message, d.now())
		_ = d.store.Put(ctx, record, d.owner)
		if d.final != nil {
			d.final(record)
		}
		return Receipt{Record: record}, &Error{Code: code, Message: message}
	}
	record.State, record.Generation, record.LastProgressAt = StateRollingOut, generation, d.now().UnixMilli()
	if err := d.store.Put(ctx, record, d.owner); err != nil {
		// Written to the Deployment and held by nobody here: the receipt
		// still says pending, and whoever takes it reads the image to see
		// that the write landed.
		return Receipt{Record: record, Applied: true}, nil
	}
	d.Adopt(d.runContext(), record)
	return Receipt{Record: record, Applied: true}, nil
}

func validRequest(request Request) error {
	if len(validation.IsDNS1123Label(request.Namespace)) != 0 || len(validation.IsDNS1123Subdomain(request.Name)) != 0 || request.UID == "" {
		return &Error{Code: CodeInvalid, Message: "namespace, name and the deployment UID from discovery are required"}
	}
	if len(validation.IsDNS1123Label(request.Container)) != 0 {
		return &Error{Code: CodeInvalid, Message: "container must name one container of the deployment"}
	}
	if request.Image == "" || len(request.Image) > MaxImageBytes || strings.IndexFunc(request.Image, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return &Error{Code: CodeInvalid, Message: "image must be a reference without spaces, at most 512 bytes"}
	}
	return nil
}

// warnings are the settings under which a change may not end as asked.
func warnings(dep Deployment) []string {
	var out []string
	if dep.Paused {
		out = append(out, WarnPaused)
	}
	if dep.Strategy == "Recreate" || dep.Replicas > 0 && dep.MaxUnavailable >= dep.Replicas {
		out = append(out, WarnNoOldServing)
	}
	return out
}

func receiptID() (string, error) {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
