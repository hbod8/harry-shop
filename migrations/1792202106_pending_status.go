package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Splits the old "unconfirmed" order status in two:
//
//	pending      a card checkout was started but not paid; cancelled if abandoned
//	unconfirmed  an order was submitted but its price isn't final (group buys)
//
// Existing unpaid card checkouts (those with a Stripe session) become pending,
// and the stats view stops counting pending checkouts as orders.
func init() {
	statuses := func(app core.App, values ...string) error {
		orders, err := app.FindCollectionByNameOrId("orders")
		if err != nil {
			return err
		}
		orders.Fields.GetByName("status").(*core.SelectField).Values = values
		return app.Save(orders)
	}

	m.Register(func(app core.App) error {
		if err := statuses(app, "pending", "unconfirmed", "confirmed", "fulfilled", "cancelled"); err != nil {
			return err
		}
		if _, err := app.DB().NewQuery(
			"UPDATE orders SET status = 'pending' WHERE status = 'unconfirmed' AND stripe_session_id != ''").
			Execute(); err != nil {
			return err
		}
		stats, err := app.FindCollectionByNameOrId("stats")
		if err != nil {
			return err
		}
		stats.ViewQuery = statsQuery
		return app.Save(stats)
	}, func(app core.App) error {
		if _, err := app.DB().NewQuery("UPDATE orders SET status = 'unconfirmed' WHERE status = 'pending'").Execute(); err != nil {
			return err
		}
		return statuses(app, "unconfirmed", "confirmed", "fulfilled", "cancelled")
	})
}
