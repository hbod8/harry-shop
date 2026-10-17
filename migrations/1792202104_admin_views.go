package migrations

import (
	"strings"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Admin views for /_/. All are locked (superusers only).
//
// customers: one row per user with their notes folded in, newest first.
// orders_unconfirmed / orders_confirmed: open orders waiting on Harry,
// oldest first, with an item summary and the order_totals figures.

const customersQuery = `
SELECT
  u.id      AS id,
  u.email   AS email,
  u.name    AS name,
  u.phone   AS phone,
  u.discord AS discord,
  u.marketing_consent AS marketing_consent,
  u.created AS created,
  CAST(COUNT(n.id) AS INTEGER) AS note_count,
  CAST(COALESCE(GROUP_CONCAT(
    date(n.created) || ' [' || COALESCE(NULLIF(n.source, ''), 'note') || '] ' || n.body,
    char(10) ORDER BY n.created DESC, n.rowid DESC
  ), '') AS TEXT) AS notes
FROM users u
LEFT JOIN user_notes n ON n.user = u.id
GROUP BY u.id`

const openOrdersQuery = `
SELECT
  o.id         AS id,
  o.user       AS user,
  o.email      AS email,
  o.name       AS name,
  u.discord    AS discord,
  o.storefront AS storefront,
  o.channel    AS channel,
  o.created    AS created,
  CAST(COALESCE((
    SELECT GROUP_CONCAT(l.quantity || '× ' || l.name, ', ')
    FROM order_lines l
    WHERE l."order" = o.id AND l.kind = 'item'
  ), '') AS TEXT) AS items,
  t.total          AS total,
  t.paid           AS paid,
  t.balance        AS balance,
  t.payment_status AS payment_status
FROM orders o
JOIN order_totals t ON t.id = o.id
JOIN users u ON u.id = o.user
WHERE o.status = 'STATUS'
ORDER BY o.created`

func init() {
	views := map[string]string{
		"customers":          customersQuery,
		"orders_unconfirmed": strings.Replace(openOrdersQuery, "STATUS", "unconfirmed", 1),
		"orders_confirmed":   strings.Replace(openOrdersQuery, "STATUS", "confirmed", 1),
	}
	names := []string{"customers", "orders_unconfirmed", "orders_confirmed"}

	m.Register(func(app core.App) error {
		for _, name := range names {
			c := core.NewViewCollection(name)
			c.ViewQuery = views[name]
			if err := app.Save(c); err != nil {
				return err
			}
		}
		return nil
	}, func(app core.App) error {
		return deleteAll(app, names...)
	})
}
