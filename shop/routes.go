package shop

import (
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

// Register wires the custom routes. Everything else goes through
// PocketBase's collection API.
//
//	POST /shop/checkout        JSON CheckoutRequest -> CheckoutResult
//	POST /shop/stripe/webhook  Stripe events (signed)
func Register(se *core.ServeEvent, cfg Config, payments Payments) {
	se.Router.POST("/shop/checkout", func(e *core.RequestEvent) error {
		var req CheckoutRequest
		if err := e.BindBody(&req); err != nil {
			return e.BadRequestError("Couldn't read the order.", err)
		}
		result, err := Checkout(e.Request.Context(), e.App, payments, req)
		var ce *CheckoutError
		if errors.As(err, &ce) {
			// written directly: PocketBase's error helpers replace custom data
			// with generic validation codes, and the cart needs the variant ids
			return e.JSON(ce.Status, map[string]any{
				"status":   ce.Status,
				"message":  ce.Message,
				"variants": ce.Variants,
			})
		}
		if err != nil {
			return e.InternalServerError("", err)
		}
		return e.JSON(http.StatusOK, result)
	})

	se.Router.POST("/shop/stripe/webhook", func(e *core.RequestEvent) error {
		payload, err := io.ReadAll(http.MaxBytesReader(e.Response, e.Request.Body, 1<<20))
		if err != nil {
			return e.BadRequestError("", err)
		}
		err = HandleStripeEvent(e.App, cfg, payload, e.Request.Header.Get("Stripe-Signature"))
		if errors.Is(err, ErrBadSignature) {
			return e.BadRequestError("Invalid signature.", nil)
		}
		if err != nil {
			// a 5xx makes Stripe retry later
			return e.InternalServerError("", err)
		}
		return e.NoContent(http.StatusOK)
	})
}

// expiryGrace is how long after expires_at the cron waits, so Stripe's own
// checkout.session.expired event normally arrives first.
const expiryGrace = 15 * time.Minute

// ReleaseExpired cancels pending card orders whose Stripe session has
// expired and releases their stock: the backstop for a missed
// checkout.session.expired event. Run it from a cron.
func ReleaseExpired(app core.App, now time.Time) (int, error) {
	cutoff, _ := types.ParseDateTime(now.Add(-expiryGrace))
	orders, err := app.FindRecordsByFilter("orders",
		"status = 'pending' && expires_at != '' && expires_at < {:cutoff}",
		"", 0, 0, map[string]any{"cutoff": cutoff})
	if err != nil {
		return 0, err
	}
	for _, o := range orders {
		if err := cancelOrder(app, o.Id); err != nil {
			return 0, err
		}
	}
	return len(orders), nil
}
