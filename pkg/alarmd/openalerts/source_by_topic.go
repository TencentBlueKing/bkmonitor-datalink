// Copyright (C) 2017-2025 Tencent. All rights reserved.
// Licensed under the MIT License.

package openalerts

import (
	"context"
	"net/url"
)

// How this deployment's event source was chosen among the target's.
const (
	SourceByTopicNotAsked   = "not_asked"
	SourceByTopicResolved   = "resolved"
	SourceByTopicUnresolved = "unresolved"
)

// Why the source could not be chosen.
const (
	SourceReasonNone       = "no_candidate_reads_the_topic"
	SourceReasonSeveral    = "several_candidates_read_the_topic"
	SourceReasonUnreadable = "a_candidate_is_unreadable"
)

// SourceByTopic is how the event source this deployment's events arrive
// under was chosen. The link names a target after the first of its sources
// that writes to the target's Redis; several sources can share it, and the
// first need not be the one that receives anything. The source is the one
// candidate that is released, enabled, not deleted and subscribed to the
// topic this deployment writes to. Listed is the link's own name for the
// target; EventSourceID is what the binding carries.
type SourceByTopic struct {
	Outcome       string            `json:"outcome"`
	Topic         string            `json:"topic,omitempty"`
	Listed        string            `json:"listed,omitempty"`
	EventSourceID string            `json:"event_source_id,omitempty"`
	Reason        string            `json:"reason,omitempty"`
	Candidates    []SourceCandidate `json:"candidates,omitempty"`
}

// SourceCandidate is one of the target's sources as its definition reads.
// Only what the choice needs is kept: the subscription's brokers and
// credentials are never read.
type SourceCandidate struct {
	EventSourceID string `json:"event_source_id"`
	Topic         string `json:"topic,omitempty"`
	Enabled       bool   `json:"enabled"`
	Published     int64  `json:"published"`
	Deleted       bool   `json:"deleted,omitempty"`
	Unreadable    bool   `json:"unreadable,omitempty"`
}

func (choice SourceByTopic) clone() SourceByTopic {
	choice.Candidates = append([]SourceCandidate(nil), choice.Candidates...)
	return choice
}

// chooseSource gives binding the source reading the output topic. When none
// or several do, or a definition cannot be read, the binding keeps what it
// had: the source chosen before while it is still a candidate, else the
// link's own name.
func (reader *HTTPReconciler) chooseSource(ctx context.Context, binding TargetBinding) TargetBinding {
	topic := reader.options.OutputTopic
	choice := SourceByTopic{Outcome: SourceByTopicNotAsked, Topic: topic, Listed: binding.EventSourceID, EventSourceID: binding.EventSourceID}
	if topic == "" {
		reader.keepChoice(choice)
		return binding
	}
	var matches []string
	unreadable := false
	for _, id := range binding.Sources {
		candidate := reader.readCandidate(ctx, id)
		choice.Candidates = append(choice.Candidates, candidate)
		switch {
		case candidate.Unreadable:
			unreadable = true
		case candidate.Topic == topic && candidate.Enabled && candidate.Published > 0 && !candidate.Deleted:
			matches = append(matches, id)
		}
	}
	switch {
	case unreadable:
		choice.Reason = SourceReasonUnreadable
	case len(matches) == 0:
		choice.Reason = SourceReasonNone
	case len(matches) > 1:
		choice.Reason = SourceReasonSeveral
	default:
		choice.Outcome, choice.EventSourceID = SourceByTopicResolved, matches[0]
		binding.EventSourceID = matches[0]
		reader.keepChoice(choice)
		return binding
	}
	choice.Outcome = SourceByTopicUnresolved
	reader.mu.Lock()
	previous := reader.chosenSource
	reader.mu.Unlock()
	if previous != "" && containsSource(binding.Sources, previous) {
		binding.EventSourceID = previous
	}
	choice.EventSourceID = binding.EventSourceID
	reader.keepChoice(choice)
	return binding
}

func (reader *HTTPReconciler) keepChoice(choice SourceByTopic) {
	reader.mu.Lock()
	defer reader.mu.Unlock()
	if choice.Outcome == SourceByTopicResolved {
		reader.chosenSource = choice.EventSourceID
	}
	reader.sourceChoice = choice
}

func (reader *HTTPReconciler) readCandidate(ctx context.Context, id string) SourceCandidate {
	var response struct {
		ID        string `json:"id"`
		Published int64  `json:"published"`
		Deleted   bool   `json:"deleted"`
		Spec      struct {
			// The link runs a source unless its definition says otherwise.
			Enabled *bool `json:"enabled"`
			Storage struct {
				Kafka struct {
					Topic string `json:"topic"`
				} `json:"kafka"`
			} `json:"storage"`
		} `json:"spec"`
	}
	err := reader.getPath(ctx, "/event-sources/"+url.PathEscape(id), nil, &response)
	reader.record(ConsoleOpEventSource, reader.now(), err)
	if err != nil || response.ID != id {
		return SourceCandidate{EventSourceID: id, Unreadable: true}
	}
	return SourceCandidate{EventSourceID: id, Topic: response.Spec.Storage.Kafka.Topic, Enabled: response.Spec.Enabled == nil || *response.Spec.Enabled,
		Published: response.Published, Deleted: response.Deleted}
}
