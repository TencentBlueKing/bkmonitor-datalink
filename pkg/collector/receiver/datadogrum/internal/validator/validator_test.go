package validator

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/TencentBlueKing/bkmonitor-datalink/pkg/collector/receiver/datadogrum/internal/model"
)

func TestValidateCommonRequiresSessionIDWhenPresent(t *testing.T) {
	v := New()
	event := &model.ActionEvent{CommonFields: model.CommonFields{
		Date:        1,
		Type:        model.EventTypeAction,
		Application: model.Application{ID: "app"},
		Session:     &model.Session{},
	}, Action: model.Action{Present: true}}
	assert.ErrorIs(t, v.Validate(event), model.ErrMissingRequiredField)
}

func TestValidateViewRequiresID(t *testing.T) {
	v := New()
	event := &model.ViewEvent{CommonFields: model.CommonFields{
		Date:        1,
		Type:        model.EventTypeView,
		Application: model.Application{ID: "app"},
	}, View: model.View{Present: true}}
	assert.ErrorIs(t, v.Validate(event), model.ErrMissingRequiredField)
}
