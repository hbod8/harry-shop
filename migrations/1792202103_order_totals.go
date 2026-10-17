package migrations

import (
	"strings"

	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// order_totals sums each order's lines (see order_lines in
// 1792202101_customers_orders.go):
//
//	total   = items + shipping + tax
//	paid    = payments - refunds
//	balance = total - paid
//
// The CASTs tell PocketBase the column types; without them computed
// columns become JSON fields.
const orderTotalsQuery = `
SELECT
  o.id                AS id,
  o.user              AS user,
  o.storefront        AS storefront,
  o.channel           AS channel,
  o.status            AS status,
  o.stripe_session_id AS stripe_session_id,
  o.created           AS created,
  CAST(TOTAL AS INTEGER)            AS total,
  CAST(PAID AS INTEGER)             AS paid,
  CAST(TOTAL - (PAID) AS INTEGER)   AS balance,
  CAST(CASE
    WHEN PAYMENTS = 0       THEN 'unpaid'
    WHEN PAID <= 0          THEN 'refunded'
    WHEN PAID < TOTAL       THEN 'partial'
    WHEN PAID = TOTAL       THEN 'paid'
    ELSE 'overpaid'
  END AS TEXT)                      AS payment_status
FROM orders o
LEFT JOIN order_lines l ON l."order" = o.id
GROUP BY o.id`

func init() {
	// TOTAL, PAYMENTS and PAID in the query stand for these sums.
	amount := "l.quantity * l.unit_cents"
	query := strings.NewReplacer(
		"TOTAL", "COALESCE(SUM(CASE WHEN l.kind IN ('item', 'shipping', 'tax') THEN "+amount+" END), 0)",
		"PAYMENTS", "COALESCE(SUM(CASE WHEN l.kind = 'payment' THEN "+amount+" END), 0)",
		"PAID", "COALESCE(SUM(CASE l.kind WHEN 'payment' THEN "+amount+" WHEN 'refund' THEN -"+amount+" END), 0)",
	).Replace(orderTotalsQuery)

	m.Register(func(app core.App) error {
		ownOrSession := rule(`(@request.auth.id != "" && user = @request.auth.id) || ` +
			`(stripe_session_id != "" && stripe_session_id = @request.query.session_id)`)
		c := core.NewViewCollection("order_totals")
		c.ListRule = ownOrSession
		c.ViewRule = ownOrSession
		c.ViewQuery = query
		return app.Save(c)
	}, func(app core.App) error {
		return deleteAll(app, "order_totals")
	})
}
