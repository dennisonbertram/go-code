package harness

import (
	"fmt"
	"time"

	"go-agent-harness/internal/forensics/redaction"
	"go-agent-harness/internal/rollout"
)

type eventDispatch struct {
	runID     string
	eventType EventType
	event     Event
	eventSeq  uint64

	dropped bool

	subscribers []subscriberDelivery

	recorderCh    chan rollout.RecordableEvent
	recorderDone  chan struct{}
	closeRecorder func()
}

type subscriberDelivery struct {
	ch    chan Event
	event Event
}

type eventJournal struct {
	runner *Runner
}

func newEventJournal(r *Runner) *eventJournal {
	return &eventJournal{runner: r}
}

func (j *eventJournal) prepareLocked(state *runState, runID string, eventType EventType, payload map[string]any) (eventDispatch, bool) {
	// Deep-clone the caller's payload so that nested maps and slices inside
	// the payload are not aliased. A shallow copy is insufficient: if the
	// caller holds a reference to a nested slice or map and mutates it after
	// emit() returns (or concurrently), the stored forensic event would
	// otherwise observe those mutations (#228).
	enriched := deepClonePayload(payload)
	if enriched == nil {
		enriched = make(map[string]any, 3)
	}
	// Inject forensic correlation fields into every event payload.
	enriched["schema_version"] = EventSchemaVersion
	enriched["conversation_id"] = state.run.ConversationID
	if _, ok := enriched["step"]; !ok {
		enriched["step"] = state.currentStep
	}

	// Seal the run for terminal events BEFORE redaction so that even if the
	// redaction pipeline drops the event, the recorder is still closed and
	// the terminated gate is still armed. Without this, a "drop" rule on
	// run.completed would leave the run unsealed forever.
	isTerminal := IsTerminalEvent(eventType)
	delivery := eventDispatch{
		runID:     runID,
		eventType: eventType,
	}
	if isTerminal {
		state.terminated = true
		delivery.recorderCh = state.recorderCh
		delivery.recorderDone = state.recorderDone
		delivery.closeRecorder = state.closeRecorderOnce
		state.recorderCh = nil
		state.recorderDone = nil
		state.closeRecorderOnce = nil
	}

	// Apply PII/secret redaction pipeline if configured.
	if j.runner.config.RedactionPipeline != nil {
		var keep bool
		enriched, keep = redaction.RedactPayload(j.runner.config.RedactionPipeline, string(eventType), enriched)
		if !keep {
			delivery.dropped = true
			return delivery, true
		}
	}

	// Deep-clone the enriched payload for immutable forensic storage.
	// This prevents any nested map/slice from being shared with subscribers,
	// the recorder, or the original caller.
	storedPayload := deepClonePayload(enriched)

	eventSeq := state.nextEventSeq
	event := Event{
		ID:        fmt.Sprintf("%s:%d", runID, eventSeq),
		RunID:     runID,
		Type:      eventType,
		Timestamp: time.Now().UTC(),
		Payload:   storedPayload,
	}
	state.nextEventSeq++
	state.events = append(state.events, event)

	delivery.event = event
	delivery.eventSeq = eventSeq

	// For non-terminal events, preserve the original fanout behavior by
	// publishing while the runner lock is still held so a concurrent cancel
	// cannot close the channel between our check and send.
	if !isTerminal {
		for ch := range state.subscribers {
			evCopy := event
			evCopy.Payload = deepClonePayload(storedPayload)
			select {
			case ch <- evCopy:
			default:
				// Drop if subscriber is too slow; event is still persisted in run history.
			}
		}
	} else {
		// Terminal events need a stronger ordering guarantee: append to the store
		// before subscribers can observe the terminal event. We still snapshot the
		// subscriber deliveries while the runner lock is held so the payload stays
		// isolated and the subscriber set is consistent for this event.
		delivery.subscribers = make([]subscriberDelivery, 0, len(state.subscribers))
		for ch := range state.subscribers {
			evCopy := event
			evCopy.Payload = deepClonePayload(storedPayload)
			delivery.subscribers = append(delivery.subscribers, subscriberDelivery{
				ch:    ch,
				event: evCopy,
			})
		}
	}

	// Capture the recorder channel under lock for non-terminal events.
	if !isTerminal {
		delivery.recorderCh = state.recorderCh
	}

	return delivery, true
}

func (j *eventJournal) publishTerminal(delivery eventDispatch) {
	j.runner.storeAppendEvent(delivery.event, delivery.eventSeq)

	for _, sub := range delivery.subscribers {
		j.runner.sendTerminalSubscriberEvent(sub.ch, sub.event)
	}
}

func (j *eventJournal) dispatch(delivery eventDispatch) {
	if delivery.dropped {
		if delivery.closeRecorder != nil {
			delivery.closeRecorder()
		}
		return
	}

	if !IsTerminalEvent(delivery.eventType) {
		j.runner.storeAppendEvent(delivery.event, delivery.eventSeq)
	}

	// Record to the JSONL rollout file via the per-run recorder goroutine.
	// The goroutine owns all writes to the file and is the only entity that
	// calls rec.Record / rec.Close, so no additional serialisation is needed.
	rev := rollout.RecordableEvent{
		ID:        delivery.event.ID,
		RunID:     delivery.event.RunID,
		Type:      string(delivery.event.Type),
		Timestamp: delivery.event.Timestamp,
		Payload:   delivery.event.Payload,
		Seq:       delivery.eventSeq,
	}
	if IsTerminalEvent(delivery.eventType) {
		if delivery.recorderCh != nil {
			sendTimer := time.NewTimer(recorderDrainTimeout)
			defer sendTimer.Stop()
			select {
			case delivery.recorderCh <- rev:
			case <-sendTimer.C:
				if j.runner.config.Logger != nil {
					j.runner.config.Logger.Error("rollout recorder: terminal send timeout, JSONL may be incomplete",
						"run_id", delivery.runID, "timeout", recorderDrainTimeout)
				}
			}
			delivery.closeRecorder()
			drainTimer := time.NewTimer(recorderDrainTimeout)
			defer drainTimer.Stop()
			select {
			case <-delivery.recorderDone:
			case <-drainTimer.C:
				if j.runner.config.Logger != nil {
					j.runner.config.Logger.Error("rollout recorder: drain timeout exceeded, JSONL may be incomplete",
						"run_id", delivery.runID, "timeout", recorderDrainTimeout)
				}
			}
		}
		return
	}

	if delivery.recorderCh != nil {
		if !safeRecorderSend(delivery.recorderCh, rev) {
			if j.runner.config.Logger != nil {
				j.runner.config.Logger.Error("rollout recorder: channel full, event dropped",
					"run_id", delivery.runID, "event_type", string(delivery.eventType), "seq", delivery.eventSeq)
			}
			dropMarker := rollout.RecordableEvent{
				ID:        fmt.Sprintf("%s:drop:%d", delivery.runID, delivery.eventSeq),
				RunID:     delivery.runID,
				Type:      string(EventRecorderDropDetected),
				Timestamp: time.Now().UTC(),
				Seq:       delivery.eventSeq,
				Payload: map[string]any{
					"dropped_event_id":   delivery.event.ID,
					"dropped_event_type": string(delivery.eventType),
					"dropped_seq":        delivery.eventSeq,
				},
			}
			safeRecorderSend(delivery.recorderCh, dropMarker)
		}
	}
}
