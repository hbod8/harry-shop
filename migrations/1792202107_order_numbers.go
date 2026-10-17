package migrations

import (
	"strings"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Sequential order numbers (#1001, #1002, ...) for customer support. The
// random id stays the order's key: it is what keeps order links
// unguessable. Numbers are assigned on create by a hook (shop.RegisterHooks);
// existing orders are numbered here in the order they were placed.
//
// Also marks "presentable" fields, which the admin UI uses to label
// related records in pickers: orders by number and email, users by email,
// variants by sku, products by title.
const firstOrderNumber = 1001

func init() {
	// The order views gain a number column. Earlier migrations can't
	// reference it, so the views are re-saved here with it added.
	withNumber := func(query, idLine string) string {
		return strings.Replace(query, idLine, idLine+"\n  o.number AS number,", 1)
	}
	views := map[string]string{
		"order_totals":       withNumber(orderTotalsSQL(), "o.id                AS id,"),
		"orders_unconfirmed": withNumber(strings.Replace(openOrdersQuery, "STATUS", "unconfirmed", 1), "o.id         AS id,"),
		"orders_confirmed":   withNumber(strings.Replace(openOrdersQuery, "STATUS", "confirmed", 1), "o.id         AS id,"),
	}

	presentable := map[string][]string{
		"orders":   {"number", "email"},
		"users":    {"email"},
		"variants": {"sku"},
		"products": {"title"},
	}
	setPresentable := func(app core.App, on bool) error {
		for collection, fields := range presentable {
			c, err := app.FindCollectionByNameOrId(collection)
			if err != nil {
				return err
			}
			for _, name := range fields {
				switch f := c.Fields.GetByName(name).(type) {
				case *core.TextField:
					f.Presentable = on
				case *core.EmailField:
					f.Presentable = on
				case *core.NumberField:
					f.Presentable = on
				}
			}
			if err := app.Save(c); err != nil {
				return err
			}
		}
		return nil
	}

	m.Register(func(app core.App) error {
		orders, err := app.FindCollectionByNameOrId("orders")
		if err != nil {
			return err
		}
		orders.Fields.Add(&core.NumberField{Name: "number", OnlyInt: true})
		orders.AddIndex("idx_orders_number", true, "number", "number > 0")
		if err := app.Save(orders); err != nil {
			return err
		}

		if _, err := app.DB().NewQuery(`
			UPDATE orders SET number = n.number FROM (
			  SELECT id, {:first} - 1 + ROW_NUMBER() OVER (ORDER BY created, rowid) AS number FROM orders
			) n WHERE orders.id = n.id`).
			Bind(map[string]any{"first": firstOrderNumber}).Execute(); err != nil {
			return err
		}

		if err := setPresentable(app, true); err != nil {
			return err
		}
		for name, query := range views {
			c, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				return err
			}
			c.ViewQuery = query
			if err := app.Save(c); err != nil {
				return err
			}
		}
		return nil
	}, func(app core.App) error {
		for name, query := range map[string]string{
			"order_totals":       orderTotalsSQL(),
			"orders_unconfirmed": strings.Replace(openOrdersQuery, "STATUS", "unconfirmed", 1),
			"orders_confirmed":   strings.Replace(openOrdersQuery, "STATUS", "confirmed", 1),
		} {
			c, err := app.FindCollectionByNameOrId(name)
			if err != nil {
				return err
			}
			c.ViewQuery = query
			if err := app.Save(c); err != nil {
				return err
			}
		}
		if err := setPresentable(app, false); err != nil {
			return err
		}
		orders, err := app.FindCollectionByNameOrId("orders")
		if err != nil {
			return err
		}
		orders.RemoveIndex("idx_orders_number")
		orders.Fields.RemoveByName("number")
		return app.Save(orders)
	})
}
