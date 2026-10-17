package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

// Customers and orders. Every customer is a users row keyed by email
// (with or without a password; OTP sign-in). Orders are readable by their
// owner, or by anyone holding the order's Stripe session id (the guest
// success page).
//
// order_lines is the order's ledger: items, shipping and tax are charges,
// payments are credits, refunds reverse payments. Amounts are always
// positive; kind sets the direction. Totals and balances are never stored,
// see the order_totals view.
func init() {
	m.Register(func(app core.App) error {
		users, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}
		users.Fields.Add(
			&core.TextField{Name: "phone", Max: 50},
			&core.TextField{Name: "discord", Max: 100},
			&core.BoolField{Name: "marketing_consent"},
			&core.DateField{Name: "consent_at"},
		)
		users.OTP.Enabled = true
		if err := app.Save(users); err != nil {
			return err
		}

		storefronts, err := app.FindCollectionByNameOrId("storefronts")
		if err != nil {
			return err
		}
		channels, err := app.FindCollectionByNameOrId("channels")
		if err != nil {
			return err
		}
		variants, err := app.FindCollectionByNameOrId("variants")
		if err != nil {
			return err
		}

		ownOrSession := rule(`(@request.auth.id != "" && user = @request.auth.id) || ` +
			`(stripe_session_id != "" && stripe_session_id = @request.query.session_id)`)
		orders := newCollection("orders", ownOrSession, ownOrSession)
		orders.Fields.Add(
			relation("storefront", storefronts, true, false),
			relation("channel", channels, true, false),
			relation("user", users, true, false),
			&core.EmailField{Name: "email", Required: true},
			&core.TextField{Name: "name", Max: 200},
			&core.TextField{Name: "phone", Max: 50},
			&core.TextField{Name: "ship_line1", Max: 200},
			&core.TextField{Name: "ship_line2", Max: 200},
			&core.TextField{Name: "ship_city", Max: 100},
			&core.TextField{Name: "ship_state", Max: 100},
			&core.TextField{Name: "ship_postal", Max: 20},
			&core.TextField{Name: "ship_country", Max: 2},
			choice("status", true, "unconfirmed", "confirmed", "fulfilled", "cancelled"),
			&core.TextField{Name: "tracking_number", Max: 100},
			&core.TextField{Name: "stripe_session_id", Max: 255},
			&core.TextField{Name: "stripe_payment_intent", Max: 255},
			&core.TextField{Name: "stripe_customer", Max: 255},
			&core.DateField{Name: "expires_at"},
		)
		addTimestamps(orders)
		orders.AddIndex("idx_orders_stripe_session", true, "stripe_session_id", "stripe_session_id != ''")
		orders.AddIndex("idx_orders_status", false, "status", "")
		if err := app.Save(orders); err != nil {
			return err
		}

		viaOrder := rule(`(@request.auth.id != "" && order.user = @request.auth.id) || ` +
			`(order.stripe_session_id != "" && order.stripe_session_id = @request.query.session_id)`)
		lines := newCollection("order_lines", viaOrder, viaOrder)
		lines.Fields.Add(
			// no cascade: deleting an order must not silently delete its payments
			relation("order", orders, true, false),
			choice("kind", true, "item", "shipping", "tax", "payment", "refund"),
			relation("variant", variants, false, false), // items only
			&core.TextField{Name: "sku", Max: 100},
			&core.TextField{Name: "name", Required: true, Max: 400},
			&core.NumberField{Name: "quantity", OnlyInt: true, Required: true, Min: types.Pointer(1.0)},
			cents("quoted_cents", false),                // items: copied at order time, never changed
			cents("unit_cents", false),                  // what is charged or credited per unit
			relation("channel", channels, false, false), // payments and refunds: how the money came in
			&core.TextField{Name: "external_ref", Max: 255},
			&core.TextField{Name: "note", Max: 2000},
		)
		addTimestamps(lines)
		lines.AddIndex("idx_order_lines_order", false, "`order`", "")
		lines.AddIndex("idx_order_lines_external_ref", true, "external_ref", "external_ref != ''")

		notes := newCollection("user_notes", nil, nil)
		notes.Fields.Add(
			relation("user", users, true, true),
			relation("order", orders, false, false),
			choice("source", false, "discord", "in_person", "email", "other"),
			&core.TextField{Name: "body", Required: true, Max: 5000},
		)
		addTimestamps(notes)

		events := newCollection("stripe_events", nil, nil)
		events.Fields.Add(
			&core.TextField{Name: "event_id", Required: true, Max: 255},
			&core.TextField{Name: "type", Max: 100},
		)
		addTimestamps(events)
		events.AddIndex("idx_stripe_events_event_id", true, "event_id", "")

		return saveAll(app, lines, notes, events)
	}, func(app core.App) error {
		if err := deleteAll(app, "stripe_events", "user_notes", "order_lines", "orders"); err != nil {
			return err
		}
		users, err := app.FindCollectionByNameOrId("users")
		if err != nil {
			return err
		}
		for _, f := range []string{"phone", "discord", "marketing_consent", "consent_at"} {
			users.Fields.RemoveByName(f)
		}
		users.OTP.Enabled = false
		return app.Save(users)
	})
}
