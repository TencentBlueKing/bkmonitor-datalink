// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package rollout

import (
	"context"
	"errors"
	"time"
)

// View is a receipt as workload.rollout answers it: the record, who drives
// it and until when, and the Deployment as it is now - read in one step.
type View struct {
	Record
	DrivenBy     string    `json:"driven_by,omitempty"`
	LeaseUntil   time.Time `json:"lease_until"`
	LeaseExpired bool      `json:"lease_expired"`
	// Live is the Deployment now; LiveError says why it could not be read.
	Live      *Live  `json:"live,omitempty"`
	LiveError string `json:"live_error,omitempty"`
}

// Live is a Deployment's rollout as read now: the container's image, the
// replica counts (old_remaining being those not yet replaced), whether it
// is paused, and its Progressing reason.
type Live struct {
	CurrentImage       string `json:"current_image"`
	Replicas           int32  `json:"replicas"`
	Updated            int32  `json:"updated"`
	Ready              int32  `json:"ready"`
	Available          int32  `json:"available"`
	OldRemaining       int32  `json:"old_remaining"`
	Paused             bool   `json:"paused"`
	Progressing        string `json:"progressing,omitempty"`
	Generation         int64  `json:"generation"`
	ObservedGeneration int64  `json:"observed_generation"`
}

// Read answers a receipt by id, or a Deployment's latest receipt by name.
// A receipt nobody holds while it is still in progress says so: its lease
// expired and nobody took it over.
func (d *Driver) Read(ctx context.Context, id, namespace, name string) (View, error) {
	var entry Entry
	var err error
	if id != "" {
		entry, err = d.store.Get(ctx, id)
	} else {
		entry, err = d.store.Latest(ctx, namespace, name)
	}
	if errors.Is(err, ErrNotFound) {
		return View{}, &Error{Code: CodeNotFound, Message: "no such receipt; receipts are kept 7 days"}
	}
	if err != nil {
		return View{}, &Error{Code: CodeStore, Message: "the receipt store could not be read"}
	}
	view := View{Record: entry.Record, DrivenBy: entry.DrivenBy, LeaseUntil: entry.LeaseUntil.UTC(),
		LeaseExpired: !Final(entry.Record.State) && entry.LeaseExpired()}
	dep, err := d.getDeployment(ctx, entry.Record.Namespace, entry.Record.Name)
	switch {
	case errors.Is(err, ErrDeploymentGone):
		view.LiveError = "the deployment no longer exists"
	case err != nil:
		view.LiveError = "the deployment could not be read"
	case dep.UID != entry.Record.UID:
		view.LiveError = "the deployment by this name is not the one the receipt is for"
	default:
		image, _ := dep.image(entry.Record.Container)
		live := &Live{CurrentImage: image, Replicas: dep.Replicas, Updated: dep.Updated, Ready: dep.Ready, Available: dep.Available,
			Paused: dep.Paused, Progressing: dep.Progressing.Reason, Generation: dep.Generation, ObservedGeneration: dep.ObservedGeneration}
		if old := dep.StatusReplicas - dep.Updated; old > 0 {
			live.OldRemaining = old
		}
		view.Live = live
	}
	return view, nil
}
