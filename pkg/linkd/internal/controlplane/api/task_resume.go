package api

import (
	"context"
	"net/http"
)

// taskResumer 是管理接口消费的恢复端口，执行代次校验由调度中心原子完成。
type taskResumer interface {
	ResumeTask(context.Context, string, string, int64, int64) error
}

func (a *API) resumeTask(w http.ResponseWriter, r *http.Request) {
	resumer, ok := a.Controller.(taskResumer)
	if !ok {
		http.Error(w, "task resume unavailable", http.StatusServiceUnavailable)
		return
	}
	var request struct {
		Version int64 `json:"expected_source_version"`
		Epoch   int64 `json:"expected_epoch"`
	}
	if err := decode(r, &request); err != nil || request.Version <= 0 || request.Epoch <= 0 {
		http.Error(w, "invalid task resume request", http.StatusBadRequest)
		return
	}
	if err := resumer.ResumeTask(r.Context(), r.PathValue("id"), r.PathValue("task"), request.Version, request.Epoch); err != nil {
		failure(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
