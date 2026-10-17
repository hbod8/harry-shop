package shop

import (
	"encoding/json"
	"fmt"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/stripe/stripe-go/v87"
	"github.com/stripe/stripe-go/v87/webhook"
)

// HandleStripeEvent verifies a webhook payload and applies it. Each event
// is recorded in stripe_events in the same transaction as its effects, so
// Stripe's retries and duplicates are no-ops.
func HandleStripeEvent(app core.App, cfg Config, payload []byte, signature string) error {
	event, err := webhook.ConstructEventWithOptions(payload, signature, cfg.WebhookSecret,
		// we read only stable fields, so accept events from any API version
		webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
	if err != nil {
		return ErrBadSignature
	}

	return app.RunInTransaction(func(tx core.App) error {
		if _, err := tx.FindFirstRecordByData("stripe_events", "event_id", event.ID); err == nil {
			return nil // already handled
		}
		events, err := tx.FindCollectionByNameOrId("stripe_events")
		if err != nil {
			return err
		}
		rec := core.NewRecord(events)
		rec.Set("event_id", event.ID)
		rec.Set("type", string(event.Type))
		if err := tx.Save(rec); err != nil {
			return err
		}

		switch event.Type {
		case "checkout.session.completed", "checkout.session.async_payment_succeeded":
			var s stripe.CheckoutSession
			if err := json.Unmarshal(event.Data.Raw, &s); err != nil {
				return err
			}
			if s.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
				return nil // a delayed payment method; wait for async_payment_succeeded
			}
			return confirmPaid(tx, &s)
		case "checkout.session.expired":
			var s stripe.CheckoutSession
			if err := json.Unmarshal(event.Data.Raw, &s); err != nil {
				return err
			}
			order, err := orderForSession(tx, &s)
			if err != nil || order == nil {
				return err
			}
			return cancelOrder(tx, order.Id)
		case "charge.refunded":
			var c stripe.Charge
			if err := json.Unmarshal(event.Data.Raw, &c); err != nil {
				return err
			}
			return recordRefund(tx, &c)
		}
		return nil
	})
}

// ErrBadSignature means the payload didn't come from Stripe.
var ErrBadSignature = fmt.Errorf("invalid stripe signature")

// orderForSession finds our order for a Stripe session; nil if it isn't ours.
func orderForSession(tx core.App, s *stripe.CheckoutSession) (*core.Record, error) {
	order, err := tx.FindFirstRecordByData("orders", "stripe_session_id", s.ID)
	if err == nil {
		return order, nil
	}
	if s.ClientReferenceID != "" {
		if order, err := tx.FindRecordById("orders", s.ClientReferenceID); err == nil {
			return order, nil
		}
	}
	tx.Logger().Warn("stripe: no order for checkout session", "session", s.ID)
	return nil, nil
}

// confirmPaid records a completed card payment: stock moves from reserved
// to sold, Stripe's shipping, tax and payment become order lines, and the
// buyer's details are copied onto the order.
func confirmPaid(tx core.App, s *stripe.CheckoutSession) error {
	order, err := orderForSession(tx, s)
	if err != nil || order == nil {
		return err
	}
	switch order.GetString("status") {
	case "confirmed", "fulfilled":
		return nil
	case "pending":
		if err := moveStock(tx, order.Id, true); err != nil {
			return err
		}
	default: // cancelled, or unexpectedly unconfirmed
		// paid after we gave up on it (e.g. the expiry cron ran first): the
		// reservation is gone, so take the stock directly
		tx.Logger().Warn("stripe: payment for an order that wasn't pending", "order", order.Id,
			"status", order.GetString("status"))
		if err := moveStock(tx, order.Id, false); err != nil {
			return err
		}
	}

	card, err := tx.FindFirstRecordByData("channels", "provider", "stripe")
	if err != nil {
		return err
	}
	ref := s.ID
	if s.PaymentIntent != nil && s.PaymentIntent.ID != "" {
		ref = s.PaymentIntent.ID
	}
	var shipping, tax int64
	if s.TotalDetails != nil {
		shipping, tax = s.TotalDetails.AmountShipping, s.TotalDetails.AmountTax
	}
	add := func(kind, name string, cents int64, data map[string]any) error {
		if cents <= 0 {
			return nil
		}
		return addLine(tx, order.Id, kind, name, cents, data)
	}
	if err := add("shipping", "Shipping", shipping, nil); err != nil {
		return err
	}
	if err := add("tax", "Sales tax", tax, nil); err != nil {
		return err
	}
	if err := add("payment", "Card payment", s.AmountTotal, map[string]any{"channel": card.Id, "external_ref": ref}); err != nil {
		return err
	}

	if items, err := itemTotal(tx, order.Id); err == nil && items+shipping+tax != s.AmountTotal {
		tx.Logger().Warn("stripe: paid total differs from order lines", "order", order.Id,
			"paid", s.AmountTotal, "lines", items+shipping+tax)
	}

	if d := s.CustomerDetails; d != nil {
		if d.Email != "" {
			order.Set("email", d.Email)
		}
		if d.Name != "" {
			order.Set("name", d.Name)
		}
		order.Set("phone", d.Phone)
	}
	if ci := s.CollectedInformation; ci != nil && ci.ShippingDetails != nil {
		if ci.ShippingDetails.Name != "" {
			order.Set("name", ci.ShippingDetails.Name)
		}
		if a := ci.ShippingDetails.Address; a != nil {
			order.Load(map[string]any{
				"ship_line1": a.Line1, "ship_line2": a.Line2, "ship_city": a.City,
				"ship_state": a.State, "ship_postal": a.PostalCode, "ship_country": a.Country,
			})
		}
	}
	if s.Customer != nil {
		order.Set("stripe_customer", s.Customer.ID)
	}
	order.Set("stripe_payment_intent", ref)
	order.Set("status", "confirmed")
	return tx.Save(order)
}

// moveStock takes an order's items out of stock; fromReserved also drops
// the reservation made at checkout.
func moveStock(tx core.App, orderID string, fromReserved bool) error {
	set := "on_hand = MAX(on_hand - q.qty, 0)"
	if fromReserved {
		set += ", reserved = MAX(reserved - q.qty, 0)"
	}
	_, err := tx.DB().NewQuery(`
		WITH q AS (
		  SELECT variant, SUM(quantity) AS qty FROM order_lines
		  WHERE "order" = {:order} AND kind = 'item' GROUP BY variant
		)
		UPDATE variants SET ` + set + ` FROM q WHERE variants.id = q.variant`).
		Bind(dbx.Params{"order": orderID}).Execute()
	return err
}

// recordRefund adds a refund line for whatever Stripe has refunded on a
// charge beyond what's already recorded. charge.refunded carries the
// cumulative amount, so partial refunds each add the difference.
func recordRefund(tx core.App, c *stripe.Charge) error {
	if c.PaymentIntent == nil || c.PaymentIntent.ID == "" {
		return nil
	}
	order, err := tx.FindFirstRecordByData("orders", "stripe_payment_intent", c.PaymentIntent.ID)
	if err != nil {
		tx.Logger().Warn("stripe: no order for refunded charge", "charge", c.ID)
		return nil
	}
	var recorded int64
	err = tx.DB().NewQuery(`
		SELECT COALESCE(SUM(quantity * unit_cents), 0) FROM order_lines
		WHERE "order" = {:order} AND kind = 'refund' AND external_ref LIKE {:prefix}`).
		Bind(dbx.Params{"order": order.Id, "prefix": c.ID + ":%"}).Row(&recorded)
	if err != nil {
		return err
	}
	if c.AmountRefunded <= recorded {
		return nil
	}
	card, err := tx.FindFirstRecordByData("channels", "provider", "stripe")
	if err != nil {
		return err
	}
	return addLine(tx, order.Id, "refund", "Card refund", c.AmountRefunded-recorded, map[string]any{
		"channel":      card.Id,
		"external_ref": fmt.Sprintf("%s:%d", c.ID, c.AmountRefunded),
	})
}

func addLine(tx core.App, orderID, kind, name string, cents int64, extra map[string]any) error {
	lines, err := tx.FindCollectionByNameOrId("order_lines")
	if err != nil {
		return err
	}
	line := core.NewRecord(lines)
	line.Load(map[string]any{"order": orderID, "kind": kind, "name": name, "quantity": 1, "unit_cents": cents})
	line.Load(extra)
	return tx.Save(line)
}

func itemTotal(tx core.App, orderID string) (int64, error) {
	var total int64
	err := tx.DB().NewQuery(`SELECT COALESCE(SUM(quantity * unit_cents), 0) FROM order_lines WHERE "order" = {:order} AND kind = 'item'`).
		Bind(dbx.Params{"order": orderID}).Row(&total)
	return total, err
}
