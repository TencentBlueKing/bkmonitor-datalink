// Copyright (C) 2017-2026 Tencent. All rights reserved.
// Licensed under the MIT License.

package metric

import (
	"sync"

	"github.com/prometheus/client_golang/prometheus"
)

// sreActionMetrics counts the SRE channel's execution and management calls.
// The label lists are the channel's, bound once at wiring (PresetSREActions),
// so every cell exists before the first call and a zero is a reading.
type sreActionMetrics struct {
	mu      sync.Mutex
	ops     []string
	results []string
	actions *prometheus.CounterVec
	// states and rollouts count image changes that ended, by final state;
	// watched is the receipts this process is watching now.
	states   []string
	rollouts *prometheus.CounterVec
	watched  prometheus.Gauge
	// receipts counts the receipt writes that sre.receipts reads back.
	receipts *prometheus.CounterVec
}

func newSREActionMetrics() *sreActionMetrics {
	return &sreActionMetrics{actions: prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "sre_actions_total",
		Help: "Execution, management and Secret-read calls the SRE channel made, by operation (pod.exec, pod.delete, " +
			"workload.restart, workload.scale, secret.get, configmap.get, workload.set_image, and workload.revert - a " +
			"revert the channel wrote itself when a changed image did not become ready) and result: done; target_changed (the Pod or workload was " +
			"replaced since it was read, and nothing was changed); forbidden (the apiserver refused the " +
			"ServiceAccount); refused (an invalid or unsupported request, a busy provider, or a command that " +
			"could not start - nothing ran); failed (an apiserver or transport failure); unknown (an execution " +
			"whose remote end is unconfirmed). Each call also writes one sre_action line naming the session, " +
			"the request, the namespace and object and the result. Every cell exists once the channel is " +
			"wired: eight operations by six results, 48 series. A secret.get line names the Secret and whether its " +
			"values were revealed, never a value.",
	}, []string{"op", "result"}),
		rollouts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "sre_rollouts_total",
			Help: "Image changes made through the SRE channel that ended, by final state, counted by the process that " +
				"ended them: unchanged (the container already ran the image), not_applied (the image was not written), " +
				"complete, reverted (the new image did not become ready and the previous one is back), revert_failed " +
				"(the previous image did not become ready either), superseded (someone else changed the image; left " +
				"as it is), target_changed (the Deployment was replaced or deleted). Each also writes one sre_rollout " +
				"line with the receipt, both images and who ended it. Reader: the operator after an image change, " +
				"through /metrics; workload.rollout reads one receipt.",
		}, []string{"state"}),
		receipts: func() *prometheus.CounterVec {
			v := prometheus.NewCounterVec(prometheus.CounterOpts{
				Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "sre_receipts_write_total",
				Help: "Receipts of SRE actions written to the shared store sre.receipts reads, by result: ok, or error (the " +
					"store refused or did not answer within its half second; the action itself went ahead, and its sre_action " +
					"line is the only record). Both cells exist from the start.",
			}, []string{"result"})
			v.WithLabelValues("ok")
			v.WithLabelValues("error")
			return v
		}(),
		watched: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: metricNamespace, Subsystem: metricSubsystem, Name: "sre_rollouts_watched",
			Help: "Image-change receipts this process is watching now: zero but in one process during a rollout. Summed " +
				"over the processes, more than the changes in progress would be a receipt watched twice.",
		}),
	}
}

// RecordSREReceiptWrite counts one receipt write by whether it landed.
func (r *Recorder) RecordSREReceiptWrite(ok bool) {
	if r == nil || r.phaseTwo.sreActions == nil {
		return
	}
	result := "error"
	if ok {
		result = "ok"
	}
	r.phaseTwo.sreActions.receipts.WithLabelValues(result).Inc()
}

// PresetSRERollouts binds the final states and creates every cell.
func (r *Recorder) PresetSRERollouts(states []string) {
	if r == nil || r.phaseTwo.sreActions == nil {
		return
	}
	m := r.phaseTwo.sreActions
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states = append([]string(nil), states...)
	for _, state := range states {
		m.rollouts.WithLabelValues(state)
	}
}

// RecordSRERollout counts one image change that ended; a state outside the
// bound list is not counted.
func (r *Recorder) RecordSRERollout(state string) {
	if r == nil || r.phaseTwo.sreActions == nil {
		return
	}
	m := r.phaseTwo.sreActions
	m.mu.Lock()
	states := m.states
	m.mu.Unlock()
	if knownLabel(states, state) {
		m.rollouts.WithLabelValues(state).Inc()
	}
}

// SetSRERolloutsWatched publishes how many receipts this process watches.
func (r *Recorder) SetSRERolloutsWatched(n int) {
	if r == nil || r.phaseTwo.sreActions == nil {
		return
	}
	r.phaseTwo.sreActions.watched.Set(float64(n))
}

// PresetSREActions binds the closed label lists and creates every cell. A nil
// recorder is a no-op.
func (r *Recorder) PresetSREActions(ops, results []string) {
	if r == nil || r.phaseTwo.sreActions == nil {
		return
	}
	m := r.phaseTwo.sreActions
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ops, m.results = append([]string(nil), ops...), append([]string(nil), results...)
	for _, op := range ops {
		for _, result := range results {
			m.actions.WithLabelValues(op, result)
		}
	}
}

// RecordSREAction counts one call; a label outside the bound lists is not
// counted rather than creating a series the bound says cannot exist.
func (r *Recorder) RecordSREAction(op, result string) {
	if r == nil || r.phaseTwo.sreActions == nil {
		return
	}
	m := r.phaseTwo.sreActions
	m.mu.Lock()
	ops, results := m.ops, m.results
	m.mu.Unlock()
	if !knownLabel(ops, op) || !knownLabel(results, result) {
		return
	}
	m.actions.WithLabelValues(op, result).Inc()
}
