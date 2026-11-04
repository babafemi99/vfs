package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/babafem99/vfs/tradeapp/internal/domain"
	"github.com/babafem99/vfs/tradeapp/internal/provider"
)

// ReceiveProviderEvent stores a (signature-checked) webhook in the inbox and
// queues a job to process it, in one transaction. A duplicate event id is
// accepted and ignored: duplicate reports it.
func (s *Service) ReceiveProviderEvent(ctx context.Context, body []byte) (duplicate bool, err error) {
	var payload provider.Event
	if err := json.Unmarshal(body, &payload); err != nil {
		return false, fmt.Errorf("%w: webhook body is not valid JSON", domain.ErrInvalidArgument)
	}
	if payload.ID == "" || payload.Type == "" {
		return false, fmt.Errorf("%w: webhook needs id and type", domain.ErrInvalidArgument)
	}

	event := domain.ProviderEvent{EventID: payload.ID, Type: payload.Type, Payload: body}
	if o := payload.Data.Order; o != nil {
		event.ProviderOrderID = &o.ID
		event.ClientOrderID = &o.ClientOrderID
	}

	queued := false
	err = s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		err := s.eventRepo.CreateEvent(ctx, tx, event)
		if errors.Is(err, domain.ErrDuplicate) {
			return nil // already received; already queued
		}
		if err != nil {
			return err
		}

		queued = true
		return s.jobRepo.CreateJob(ctx, tx, s.newJob(domain.JobProcessEvent, nil, &event.EventID))
	})
	if err != nil {
		return false, err
	}

	if !queued {
		s.log.Info("webhook duplicate ignored", "event_id", payload.ID, "type", payload.Type)
		return true, nil
	}
	s.log.Info("webhook stored", "event_id", payload.ID, "type", payload.Type)
	s.notifyJobsReady()
	return false, nil
}

// processEvent is the process_event job: apply one inbox event to its order
// in one transaction, then mark the event processed.
func (s *Service) processEvent(ctx context.Context, eventID string) error {
	return s.repo.RunInTx(ctx, func(tx pgx.Tx) error {
		event, err := s.eventRepo.GetEventForUpdate(ctx, tx, eventID)
		if err != nil {
			return err
		}
		if event.ProcessedAt != nil {
			return nil
		}

		var payload provider.Event
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			return s.markEventProcessed(ctx, tx, eventID, "bad_payload")
		}

		// Events without an order (quote.expired) need nothing from us.
		snap := payload.Data.Order
		if snap == nil {
			return s.markEventProcessed(ctx, tx, eventID, "")
		}

		order, err := s.findOrderForUpdate(ctx, tx, *snap)
		if errors.Is(err, domain.ErrNotFound) {
			s.log.Warn("event for an unknown order", "event_id", eventID, "type", payload.Type,
				"provider_order_id", snap.ID, "client_order_id", snap.ClientOrderID)
			return s.markEventProcessed(ctx, tx, eventID, "unknown_order")
		}
		if err != nil {
			return err
		}

		var fills []provider.Fill
		if payload.Data.Fill != nil {
			fills = append(fills, *payload.Data.Fill)
		}
		cancelRejected := payload.Type == domain.EventOrderCancelRejected

		if err := s.applySnapshot(ctx, tx, order, *snap, fills, cancelRejected); err != nil {
			return err
		}
		attrs := []any{"event_id", eventID, "type", payload.Type, "order_id", order.ID,
			"from", order.Status, "provider_status", snap.Status, "version", snap.Version, "filled", snap.FilledQty}
		if f := payload.Data.Fill; f != nil {
			attrs = append(attrs, "fill_id", f.ID, "fill_qty", f.Qty, "fill_price", f.Price)
		}
		s.log.Info("event applied", attrs...)
		return s.markEventProcessed(ctx, tx, eventID, "")
	})
}

// findOrderForUpdate finds and locks our order for a provider snapshot: by
// the provider's id, or by client_order_id (our id) if we haven't stored the
// provider's id yet.
func (s *Service) findOrderForUpdate(ctx context.Context, tx pgx.Tx, snap provider.Order) (domain.Order, error) {
	order, err := s.orderRepo.GetOrderByProviderIDForUpdate(ctx, tx, snap.ID)
	if !errors.Is(err, domain.ErrNotFound) {
		return order, err
	}
	id, ok := normalizeOrderID(snap.ClientOrderID)
	if !ok {
		return domain.Order{}, domain.ErrNotFound
	}
	return s.orderRepo.GetOrderForUpdate(ctx, tx, id)
}

func (s *Service) markEventProcessed(ctx context.Context, tx pgx.Tx, eventID, note string) error {
	var noteText *string
	if note != "" {
		noteText = &note
	}
	return s.eventRepo.UpdateEventProcessed(ctx, tx, eventID, s.now(), noteText)
}
