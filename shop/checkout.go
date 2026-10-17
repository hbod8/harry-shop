package shop

import (
	"context"
	"fmt"
	"net/http"
	"net/mail"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

const (
	maxCartLines   = 20
	maxLineQty     = 99
	reservationFor = 30 * time.Minute // Stripe's minimum session lifetime
)

// CheckoutRequest is all the browser sends: variant ids and quantities and
// who is ordering. Prices and the payment channel come from the database.
type CheckoutRequest struct {
	Lines []CheckoutLine `json:"lines"`
	Email string         `json:"email"`
	Name  string         `json:"name"`
}

type CheckoutLine struct {
	Variant string `json:"variant"`
	Qty     int    `json:"qty"`
}

// CheckoutResult has URL set for card orders (the Stripe page) and only
// OrderID for manual orders.
type CheckoutResult struct {
	OrderID string `json:"order"`
	Number  int    `json:"number"`
	URL     string `json:"url,omitempty"`
}

// CheckoutError carries the HTTP status and, where useful, the variants
// at fault (sold out, or not payable through the chosen channel).
type CheckoutError struct {
	Status   int
	Message  string
	Variants []string
}

func (e *CheckoutError) Error() string { return e.Message }

func badRequest(format string, args ...any) error {
	return &CheckoutError{Status: http.StatusBadRequest, Message: fmt.Sprintf(format, args...)}
}

// pricedLine is a request line with everything read from the database.
type pricedLine struct {
	variant *core.Record
	product *core.Record
	qty     int
}

func (l pricedLine) name() string {
	return l.product.GetString("title") + " — " + l.variant.GetString("name")
}

// Checkout validates and reprices the request, then creates the order. Card
// orders are pending: they reserve stock and get a Stripe session. Manual
// orders are unconfirmed: they reserve nothing and wait for Harry.
func Checkout(ctx context.Context, app core.App, payments Payments, req CheckoutRequest) (CheckoutResult, error) {
	email, name, err := validateCustomer(req)
	if err != nil {
		return CheckoutResult{}, err
	}

	lines, err := priceLines(app, req.Lines)
	if err != nil {
		return CheckoutResult{}, err
	}

	// every product has exactly one channel, and priceLines checked they match
	channel, err := app.FindRecordById("channels", lines[0].product.GetString("channel"))
	if err != nil || !channel.GetBool("active") {
		return CheckoutResult{}, badRequest("These items can't be ordered right now.")
	}
	isCard := channel.GetString("provider") == "stripe"
	if isCard && payments == nil {
		return CheckoutResult{}, &CheckoutError{Status: http.StatusServiceUnavailable, Message: "Card payments aren't available right now."}
	}
	if isCard {
		if soldOut := checkStock(lines); len(soldOut) > 0 {
			return CheckoutResult{}, &CheckoutError{Status: http.StatusConflict, Message: "Some items are no longer in stock.", Variants: soldOut}
		}
	}

	var expiresAt time.Time
	if isCard {
		expiresAt = time.Now().Add(reservationFor)
	}

	var order *core.Record
	err = app.RunInTransaction(func(tx core.App) error {
		user, err := findOrCreateUser(tx, email, name)
		if err != nil {
			return err
		}
		order, err = createOrder(tx, channel, user, email, name, lines, expiresAt)
		if err != nil {
			return err
		}
		if isCard {
			return reserve(tx, lines)
		}
		return nil
	})
	if err != nil {
		return CheckoutResult{}, err
	}

	if !isCard {
		return CheckoutResult{OrderID: order.Id, Number: order.GetInt("number")}, nil
	}

	session, err := payments.CreateSession(ctx, sessionRequest(order, email, lines, expiresAt))
	if err != nil {
		app.Logger().Error("stripe: creating checkout session", "order", order.Id, "error", err)
		if cancelErr := cancelOrder(app, order.Id); cancelErr != nil {
			app.Logger().Error("cancelling order after stripe failure", "order", order.Id, "error", cancelErr)
		}
		return CheckoutResult{}, &CheckoutError{Status: http.StatusBadGateway, Message: "Couldn't reach the payment provider. Please try again."}
	}

	order.Set("stripe_session_id", session.ID)
	if err := app.Save(order); err != nil {
		return CheckoutResult{}, err
	}
	return CheckoutResult{OrderID: order.Id, Number: order.GetInt("number"), URL: session.URL}, nil
}

func validateCustomer(req CheckoutRequest) (email, name string, err error) {
	addr, perr := mail.ParseAddress(strings.TrimSpace(req.Email))
	if perr != nil || addr.Address != strings.TrimSpace(req.Email) {
		return "", "", badRequest("Please enter a valid email address.")
	}
	name = strings.TrimSpace(req.Name)
	if name == "" || len(name) > 200 {
		return "", "", badRequest("Please enter your name.")
	}
	return strings.ToLower(addr.Address), name, nil
}

// priceLines merges duplicate variants and loads every variant and product,
// rejecting unknown or inactive ones and carts whose products are paid
// through different channels.
func priceLines(app core.App, req []CheckoutLine) ([]pricedLine, error) {
	if len(req) == 0 {
		return nil, badRequest("Your cart is empty.")
	}
	qty := map[string]int{}
	var order []string
	for _, l := range req {
		if l.Qty < 1 || l.Qty > maxLineQty {
			return nil, badRequest("Quantities must be between 1 and %d.", maxLineQty)
		}
		if _, seen := qty[l.Variant]; !seen {
			order = append(order, l.Variant)
		}
		qty[l.Variant] += l.Qty
	}
	if len(order) > maxCartLines {
		return nil, badRequest("A cart can have at most %d lines.", maxCartLines)
	}

	var lines []pricedLine
	var wrongChannel []string
	storefront, channel := "", ""
	for _, id := range order {
		variant, err := app.FindRecordById("variants", id)
		if err != nil {
			return nil, &CheckoutError{Status: http.StatusBadRequest, Message: "An item in your cart no longer exists.", Variants: []string{id}}
		}
		product, err := app.FindRecordById("products", variant.GetString("product"))
		if err != nil || !product.GetBool("active") {
			return nil, &CheckoutError{Status: http.StatusBadRequest, Message: "An item in your cart is no longer available.", Variants: []string{id}}
		}
		if storefront == "" {
			storefront = product.GetString("storefront")
		} else if product.GetString("storefront") != storefront {
			return nil, badRequest("Items from different shops must be ordered separately.")
		}
		if channel == "" {
			channel = product.GetString("channel")
		} else if product.GetString("channel") != channel {
			wrongChannel = append(wrongChannel, id)
		}
		if qty[id] > maxLineQty {
			return nil, badRequest("Quantities must be between 1 and %d.", maxLineQty)
		}
		lines = append(lines, pricedLine{variant: variant, product: product, qty: qty[id]})
	}
	if len(wrongChannel) > 0 {
		return nil, &CheckoutError{Status: http.StatusBadRequest, Message: "Some items are paid for differently. Please order them separately.", Variants: wrongChannel}
	}
	return lines, nil
}

// checkStock is a fast pre-check; reserve is the authoritative one.
func checkStock(lines []pricedLine) []string {
	var soldOut []string
	for _, l := range lines {
		if l.variant.GetInt("on_hand")-l.variant.GetInt("reserved") < l.qty {
			soldOut = append(soldOut, l.variant.Id)
		}
	}
	return soldOut
}

func findOrCreateUser(tx core.App, email, name string) (*core.Record, error) {
	if user, err := tx.FindAuthRecordByEmail("users", email); err == nil {
		return user, nil
	}
	users, err := tx.FindCollectionByNameOrId("users")
	if err != nil {
		return nil, err
	}
	user := core.NewRecord(users)
	user.SetEmail(email)
	user.SetRandomPassword()
	user.Set("name", name)
	return user, tx.Save(user)
}

func createOrder(tx core.App, channel, user *core.Record, email, name string, lines []pricedLine, expiresAt time.Time) (*core.Record, error) {
	orders, err := tx.FindCollectionByNameOrId("orders")
	if err != nil {
		return nil, err
	}
	status := "unconfirmed"
	if !expiresAt.IsZero() {
		status = "pending" // a card checkout: cancelled if never paid
	}
	order := core.NewRecord(orders)
	order.Load(map[string]any{
		"storefront": lines[0].product.GetString("storefront"),
		"channel":    channel.Id,
		"user":       user.Id,
		"email":      email,
		"name":       name,
		"status":     status,
	})
	if !expiresAt.IsZero() {
		dt, _ := types.ParseDateTime(expiresAt)
		order.Set("expires_at", dt)
	}
	if err := tx.Save(order); err != nil {
		return nil, err
	}

	orderLines, err := tx.FindCollectionByNameOrId("order_lines")
	if err != nil {
		return nil, err
	}
	for _, l := range lines {
		price := l.variant.GetInt("price_cents")
		line := core.NewRecord(orderLines)
		line.Load(map[string]any{
			"order":        order.Id,
			"kind":         "item",
			"variant":      l.variant.Id,
			"sku":          l.variant.GetString("sku"),
			"name":         l.name(),
			"quantity":     l.qty,
			"quoted_cents": price,
			"unit_cents":   price,
		})
		if err := tx.Save(line); err != nil {
			return nil, err
		}
	}
	return order, nil
}

// reserve holds stock for a card order. The WHERE clause makes it safe
// against concurrent checkouts: a line that no longer fits matches no row.
func reserve(tx core.App, lines []pricedLine) error {
	var soldOut []string
	for _, l := range lines {
		res, err := tx.DB().NewQuery(
			"UPDATE variants SET reserved = reserved + {:q} WHERE id = {:id} AND on_hand - reserved >= {:q}").
			Bind(dbx.Params{"q": l.qty, "id": l.variant.Id}).Execute()
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			soldOut = append(soldOut, l.variant.Id)
		}
	}
	if len(soldOut) > 0 {
		return &CheckoutError{Status: http.StatusConflict, Message: "Some items are no longer in stock.", Variants: soldOut}
	}
	return nil
}

// release returns an order's reserved stock.
func release(tx core.App, orderID string) error {
	_, err := tx.DB().NewQuery(`
		UPDATE variants SET reserved = MAX(reserved - (
		  SELECT COALESCE(SUM(l.quantity), 0) FROM order_lines l
		  WHERE l."order" = {:order} AND l.kind = 'item' AND l.variant = variants.id
		), 0)
		WHERE id IN (SELECT variant FROM order_lines WHERE "order" = {:order} AND kind = 'item')`).
		Bind(dbx.Params{"order": orderID}).Execute()
	return err
}

// cancelOrder releases a pending card order's stock and cancels it.
func cancelOrder(app core.App, orderID string) error {
	return app.RunInTransaction(func(tx core.App) error {
		order, err := tx.FindRecordById("orders", orderID)
		if err != nil {
			return err
		}
		if order.GetString("status") != "pending" {
			return nil
		}
		if err := release(tx, orderID); err != nil {
			return err
		}
		order.Set("status", "cancelled")
		return tx.Save(order)
	})
}

func sessionRequest(order *core.Record, email string, lines []pricedLine, expiresAt time.Time) SessionRequest {
	req := SessionRequest{OrderID: order.Id, Email: email, ExpiresAt: expiresAt}
	for _, l := range lines {
		req.Lines = append(req.Lines, SessionLine{
			Name:      l.name(),
			TaxCode:   l.product.GetString("tax_code"),
			UnitCents: int64(l.variant.GetInt("price_cents")),
			Quantity:  int64(l.qty),
		})
	}
	return req
}
