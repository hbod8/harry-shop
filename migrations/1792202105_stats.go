package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// stats: one row per period (admin only).
//
//	orders              orders placed in the period, excluding cancelled orders
//	                    and pending (unpaid) card checkouts
//	revenue             item lines of confirmed and fulfilled orders placed in
//	                    the period, minus their refunds; excludes tax, shipping
//	                    and pending or unconfirmed orders (unpaid, estimates)
//	new_customers       customers whose first counted order is in the period
//	returning_customers customers who ordered in the period and had ordered before it
//
// Periods use the server's local time ("localtime"), so production must run
// with TZ=America/Los_Angeles for calendar months to start at local midnight.
const statsQuery = `
SELECT
  p.id                      AS id,
  CAST(p.position AS INTEGER) AS position,
  CAST(p.label AS TEXT)     AS label,
  CAST(p.start AS TEXT)     AS since,
  CAST((
    SELECT COUNT(*) FROM orders o
    WHERE o.status NOT IN ('cancelled', 'pending') AND datetime(o.created, 'localtime') >= p.start
  ) AS INTEGER) AS orders,
  CAST((
    SELECT COALESCE(SUM(CASE l.kind
      WHEN 'item'   THEN l.quantity * l.unit_cents
      WHEN 'refund' THEN -l.quantity * l.unit_cents END), 0)
    FROM order_lines l
    JOIN orders o ON o.id = l."order"
    WHERE l.kind IN ('item', 'refund') AND o.status IN ('confirmed', 'fulfilled')
      AND datetime(o.created, 'localtime') >= p.start
  ) AS INTEGER) AS revenue,
  CAST((
    SELECT COUNT(*) FROM (
      SELECT MIN(o.created) AS first_order FROM orders o
      WHERE o.status NOT IN ('cancelled', 'pending') GROUP BY o.user
    ) f
    WHERE datetime(f.first_order, 'localtime') >= p.start
  ) AS INTEGER) AS new_customers,
  CAST((
    SELECT COUNT(DISTINCT o.user) FROM orders o
    WHERE o.status NOT IN ('cancelled', 'pending') AND datetime(o.created, 'localtime') >= p.start
      AND EXISTS (
        SELECT 1 FROM orders e
        WHERE e.user = o.user AND e.status NOT IN ('cancelled', 'pending')
          AND datetime(e.created, 'localtime') < p.start
      )
  ) AS INTEGER) AS returning_customers
FROM (
  SELECT 'last_7_days' AS id, 1 AS position, 'Last 7 days' AS label,
         datetime('now', 'localtime', '-7 days') AS start
  UNION ALL
  SELECT 'last_30_days', 2, 'Last 30 days', datetime('now', 'localtime', '-30 days')
  UNION ALL
  SELECT 'this_month', 3, 'This month', datetime('now', 'localtime', 'start of month')
  UNION ALL
  SELECT 'this_quarter', 4, 'This quarter', datetime('now', 'localtime', 'start of month',
         printf('-%d months', (CAST(strftime('%m', 'now', 'localtime') AS INTEGER) - 1) % 3))
  UNION ALL
  SELECT 'this_year', 5, 'This year', datetime('now', 'localtime', 'start of year')
) p
ORDER BY p.position`

func init() {
	m.Register(func(app core.App) error {
		c := core.NewViewCollection("stats")
		c.ViewQuery = statsQuery
		return app.Save(c)
	}, func(app core.App) error {
		return deleteAll(app, "stats")
	})
}
