package scheduler

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"linkd/internal/consume"
	"linkd/internal/domain"
	"linkd/internal/enrich"
	"linkd/internal/lifecycle"
	"linkd/internal/store/memory"
	"linkd/internal/store/storetest"
)

type fallbackContentBuilder func(context.Context, domain.Event, domain.EventEvaluation, domain.Alert) (string, error)

func (f fallbackContentBuilder) BuildContent(ctx context.Context, e domain.Event, v domain.EventEvaluation, a domain.Alert) (string, error) {
	return f(ctx, e, v, a)
}

type contentSeverity struct{}

func (contentSeverity) Priority(name string) (int, bool) {
	priority, ok := map[string]int{"critical": 1, "warning": 2, "info": 3}[name]
	return priority, ok
}

func TestContentFailureCreatesAlertsAndDrainsMailbox(t *testing.T) {
	for _, mode := range []string{"error", "panic", "empty output", "invalid output", "empty source"} {
		t.Run(mode, func(t *testing.T) {
			repo := memory.New()
			var logs bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&logs, nil))
			first := storetest.Event("tenant-1", "event-1", "fp-1", "warning")
			first.EventSourceID = "source-1"
			first.Content = "private source content"
			if mode == "empty source" {
				first.Content = ""
			}
			second := first.Clone()
			second.EventID = "event-2"
			second.Evaluations[0].Severity = "critical"
			for _, event := range []domain.Event{first, second} {
				if _, err := repo.CreateEvent(t.Context(), event); err != nil {
					t.Fatal(err)
				}
			}
			builder := fallbackContentBuilder(func(_ context.Context, event domain.Event, _ domain.EventEvaluation, _ domain.Alert) (string, error) {
				if event.EventID == "event-2" {
					return "second generated content", nil
				}
				switch mode {
				case "panic":
					panic("secret panic")
				case "empty output":
					return "", nil
				case "invalid output":
					return strings.Repeat("x", (1<<20)+1), nil
				default:
					return "", errors.New("secret dependency payload")
				}
			})
			processor, err := lifecycle.NewProcessor(repo, lifecycle.NoopRecentAlertCache{}, lifecycle.DeterministicAlertIDGenerator{}, enrich.NoopEnricher{}, nil, contentSeverity{}, lifecycle.SystemClock{}, logger, lifecycle.WithAlertContentBuilder(builder))
			if err != nil {
				t.Fatal(err)
			}
			mailbox := &fakeMailbox{ids: []string{"event-1", "event-2"}}
			handler, err := NewHandler(repo, mailbox, processor, &fakeLocker{}, DefaultConfig(), logger)
			if err != nil {
				t.Fatal(err)
			}
			result := handler.Handle(t.Context(), signalMessage(t, first))
			if result.Kind != consume.OutcomeComplete || len(mailbox.ids) != 0 {
				t.Fatalf("result=%+v mailbox=%v", result, mailbox.ids)
			}
			for _, event := range []domain.Event{first, second} {
				kept, err := repo.GetEvent(t.Context(), event.BKTenantID, event.EventID)
				if err != nil || kept.Processing.State != domain.EventProcessStateAccepted || len(kept.Event.RelatedAlertIDs) == 0 {
					t.Fatalf("event=%+v error=%v", kept, err)
				}
				var created domain.Alert
				for _, id := range kept.Event.RelatedAlertIDs {
					alert, err := repo.GetAlert(t.Context(), event.BKTenantID, id)
					if err != nil {
						t.Fatal(err)
					}
					if alert.Alert.TriggerEventID == event.EventID {
						created = alert.Alert
					}
				}
				want := first.Content
				if mode == "empty source" {
					want = enrich.DefaultAlertContent
				}
				if event.EventID == "event-2" {
					want = "second generated content"
				}
				if created.Content != want || kept.Event.Content != event.Content {
					t.Fatalf("content=%q want=%q", created.Content, want)
				}
			}
			if strings.Contains(logs.String(), "discard lifecycle event") || strings.Contains(logs.String(), "secret") || !strings.Contains(logs.String(), "fallback") {
				t.Fatalf("diagnostic=%s", logs.String())
			}
		})
	}
}
