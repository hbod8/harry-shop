package shop_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	_ "github.com/hbod8/harry-shop/migrations"
	"github.com/hbod8/harry-shop/shop"
)

func seededApp(t testing.TB) *tests.TestApp {
	t.Helper()
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	shop.RegisterHooks(app)
	if err := shop.Seed(app); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(app.Cleanup)
	return app
}

func variant(t testing.TB, app core.App, sku string) *core.Record {
	t.Helper()
	v, err := app.FindFirstRecordByData("variants", "sku", sku)
	if err != nil {
		t.Fatalf("variant %s: %v", sku, err)
	}
	return v
}

func countOrders(t testing.TB, app core.App) int {
	t.Helper()
	n, err := app.CountRecords("orders")
	if err != nil {
		t.Fatal(err)
	}
	return int(n)
}

// fakePayments stands in for Stripe.
type fakePayments struct {
	mu    sync.Mutex
	calls []shop.SessionRequest
	err   error
}

func (f *fakePayments) CreateSession(_ context.Context, req shop.SessionRequest) (shop.Session, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return shop.Session{}, f.err
	}
	f.calls = append(f.calls, req)
	n := len(f.calls)
	return shop.Session{ID: fmt.Sprintf("cs_test_%d", n), URL: fmt.Sprintf("https://checkout.stripe.test/%d", n)}, nil
}

func cardRequest(lines ...shop.CheckoutLine) shop.CheckoutRequest {
	return shop.CheckoutRequest{Lines: lines, Email: "New.Buyer@Example.com", Name: "New Buyer"}
}

func TestCheckoutCard(t *testing.T) {
	app := seededApp(t)
	pay := &fakePayments{}
	teeM := variant(t, app, "TEE-LOGO-M")

	res, err := shop.Checkout(context.Background(), app, pay, cardRequest(
		shop.CheckoutLine{Variant: teeM.Id, Qty: 1},
		shop.CheckoutLine{Variant: teeM.Id, Qty: 1}, // duplicates merge
	))
	if err != nil {
		t.Fatal(err)
	}
	if res.URL != "https://checkout.stripe.test/1" {
		t.Errorf("url = %q", res.URL)
	}

	order, err := app.FindRecordById("orders", res.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	if order.GetString("status") != "pending" || order.GetString("stripe_session_id") != "cs_test_1" ||
		order.GetDateTime("expires_at").IsZero() || order.GetString("email") != "new.buyer@example.com" {
		t.Errorf("order = %v", order.PublicExport())
	}
	user, err := app.FindRecordById("users", order.GetString("user"))
	if err != nil || user.Email() != "new.buyer@example.com" {
		t.Errorf("user for order: %v %v", user, err)
	}

	lines, err := app.FindAllRecords("order_lines")
	if err != nil {
		t.Fatal(err)
	}
	var item *core.Record
	for _, l := range lines {
		if l.GetString("order") == order.Id {
			item = l
		}
	}
	if item == nil || item.GetString("kind") != "item" || item.GetInt("quantity") != 2 ||
		item.GetInt("quoted_cents") != 2800 || item.GetInt("unit_cents") != 2800 ||
		item.GetString("name") != "Harry Logo Tee — M" {
		t.Errorf("item line = %v", item)
	}

	if got := variant(t, app, "TEE-LOGO-M").GetInt("reserved"); got != 2 {
		t.Errorf("reserved = %d, want 2", got)
	}
	if len(pay.calls) != 1 || len(pay.calls[0].Lines) != 1 ||
		pay.calls[0].Lines[0] != (shop.SessionLine{Name: "Harry Logo Tee — M", UnitCents: 2800, Quantity: 2}) ||
		pay.calls[0].OrderID != order.Id || pay.calls[0].Email != "new.buyer@example.com" {
		t.Errorf("stripe calls = %+v", pay.calls)
	}
}

func TestCheckoutRejects(t *testing.T) {
	app := seededApp(t)
	pike11 := variant(t, app, "PRINT-PIKE-11X14") // 2 on hand
	teeL := variant(t, app, "TEE-LOGO-L")         // sold out
	teeM := variant(t, app, "TEE-LOGO-M")
	retired := variant(t, app, "PRINT-RETIRED-8X10") // inactive product
	dev := variant(t, app, "CHEM-DEV-R1-1L")         // manual only

	line := func(v *core.Record, qty int) shop.CheckoutLine { return shop.CheckoutLine{Variant: v.Id, Qty: qty} }
	cases := []struct {
		name     string
		req      shop.CheckoutRequest
		payments shop.Payments
		status   int
		variants []string
	}{
		{"empty cart", cardRequest(), &fakePayments{}, 400, nil},
		{"unknown variant", cardRequest(shop.CheckoutLine{Variant: "zzzzzzzzzzzzzzz", Qty: 1}), &fakePayments{}, 400, []string{"zzzzzzzzzzzzzzz"}},
		{"inactive product", cardRequest(line(retired, 1)), &fakePayments{}, 400, []string{retired.Id}},
		{"zero quantity", cardRequest(line(teeM, 0)), &fakePayments{}, 400, nil},
		{"quantity over the limit", cardRequest(line(teeM, 60), line(teeM, 60)), &fakePayments{}, 400, nil},
		{"more than in stock", cardRequest(line(pike11, 3)), &fakePayments{}, 409, []string{pike11.Id}},
		{"sold out", cardRequest(line(teeM, 1), line(teeL, 1)), &fakePayments{}, 409, []string{teeL.Id}},
		{"card and manual items together", cardRequest(line(teeM, 1), line(dev, 1)), &fakePayments{}, 400, []string{dev.Id}},
		{"bad email", shop.CheckoutRequest{Lines: []shop.CheckoutLine{line(teeM, 1)}, Email: "not-an-email", Name: "A"}, &fakePayments{}, 400, nil},
		{"no name", shop.CheckoutRequest{Lines: []shop.CheckoutLine{line(teeM, 1)}, Email: "a@example.com", Name: "  "}, &fakePayments{}, 400, nil},
		{"card payments not configured", cardRequest(line(teeM, 1)), nil, 503, nil},
	}

	before := countOrders(t, app)
	for _, c := range cases {
		_, err := shop.Checkout(context.Background(), app, c.payments, c.req)
		var ce *shop.CheckoutError
		if !errors.As(err, &ce) || ce.Status != c.status {
			t.Errorf("%s: err = %v, want status %d", c.name, err, c.status)
			continue
		}
		if c.variants != nil && strings.Join(ce.Variants, ",") != strings.Join(c.variants, ",") {
			t.Errorf("%s: variants = %v, want %v", c.name, ce.Variants, c.variants)
		}
	}
	if got := countOrders(t, app); got != before {
		t.Errorf("rejected checkouts created %d orders", got-before)
	}
	for _, sku := range []string{"PRINT-PIKE-11X14", "TEE-LOGO-M", "TEE-LOGO-L"} {
		if r := variant(t, app, sku).GetInt("reserved"); r != 0 {
			t.Errorf("%s reserved = %d after rejected checkouts", sku, r)
		}
	}
}

func TestCheckoutManual(t *testing.T) {
	app := seededApp(t)
	pay := &fakePayments{}
	dev := variant(t, app, "CHEM-DEV-R1-1L")
	sam, err := app.FindAuthRecordByEmail("users", "sam@example.com")
	if err != nil {
		t.Fatal(err)
	}

	res, err := shop.Checkout(context.Background(), app, pay, shop.CheckoutRequest{
		Lines: []shop.CheckoutLine{{Variant: dev.Id, Qty: 3}},
		Email: "Sam@Example.com", Name: "Sam",
	})
	if err != nil {
		t.Fatal(err)
	}
	order, err := app.FindRecordById("orders", res.OrderID)
	if err != nil {
		t.Fatal(err)
	}
	if res.URL != "" || len(pay.calls) != 0 {
		t.Errorf("manual order went to stripe: %+v %+v", res, pay.calls)
	}
	if order.GetString("user") != sam.Id {
		t.Errorf("order user = %s, want existing user %s", order.GetString("user"), sam.Id)
	}
	if !order.GetDateTime("expires_at").IsZero() || order.GetString("status") != "unconfirmed" {
		t.Errorf("manual order: status %s, expires %v", order.GetString("status"), order.GetDateTime("expires_at"))
	}
	if r := variant(t, app, "CHEM-DEV-R1-1L").GetInt("reserved"); r != 0 {
		t.Errorf("manual order reserved %d", r)
	}
}

func TestCheckoutLastUnitConcurrently(t *testing.T) {
	app := seededApp(t)
	pike11 := variant(t, app, "PRINT-PIKE-11X14")
	pike11.Set("on_hand", 1)
	if err := app.Save(pike11); err != nil {
		t.Fatal(err)
	}

	pay := &fakePayments{}
	const buyers = 10
	var wg sync.WaitGroup
	var mu sync.Mutex
	won, soldOut := 0, 0
	for i := range buyers {
		wg.Go(func() {
			req := cardRequest(shop.CheckoutLine{Variant: pike11.Id, Qty: 1})
			req.Email = fmt.Sprintf("buyer%d@example.com", i)
			_, err := shop.Checkout(context.Background(), app, pay, req)
			var ce *shop.CheckoutError
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				won++
			case errors.As(err, &ce) && ce.Status == http.StatusConflict:
				soldOut++
			default:
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
	wg.Wait()

	if won != 1 || soldOut != buyers-1 {
		t.Errorf("won %d, sold out %d; want 1 and %d", won, soldOut, buyers-1)
	}
	if r := variant(t, app, "PRINT-PIKE-11X14").GetInt("reserved"); r != 1 {
		t.Errorf("reserved = %d, want 1", r)
	}
}

func TestCheckoutStripeFailure(t *testing.T) {
	app := seededApp(t)
	teeM := variant(t, app, "TEE-LOGO-M")

	_, err := shop.Checkout(context.Background(), app, &fakePayments{err: errors.New("stripe down")},
		cardRequest(shop.CheckoutLine{Variant: teeM.Id, Qty: 2}))
	var ce *shop.CheckoutError
	if !errors.As(err, &ce) || ce.Status != http.StatusBadGateway {
		t.Fatalf("err = %v, want 502", err)
	}
	if r := variant(t, app, "TEE-LOGO-M").GetInt("reserved"); r != 0 {
		t.Errorf("reserved = %d after stripe failure, want 0", r)
	}
	cancelled, err := app.FindRecordsByFilter("orders", "status = 'cancelled'", "", 0, 0)
	if err != nil || len(cancelled) != 1 {
		t.Errorf("cancelled orders = %d (%v), want 1", len(cancelled), err)
	}
}

func TestReleaseExpired(t *testing.T) {
	app := seededApp(t)
	teeM := variant(t, app, "TEE-LOGO-M")
	res, err := shop.Checkout(context.Background(), app, &fakePayments{}, cardRequest(shop.CheckoutLine{Variant: teeM.Id, Qty: 2}))
	if err != nil {
		t.Fatal(err)
	}

	// 31 minutes: the session has expired but the grace period hasn't passed
	if n, err := shop.ReleaseExpired(app, time.Now().Add(31*time.Minute)); err != nil || n != 0 {
		t.Fatalf("released %d (%v) within the grace period", n, err)
	}
	if n, err := shop.ReleaseExpired(app, time.Now().Add(50*time.Minute)); err != nil || n != 1 {
		t.Fatalf("released %d (%v), want 1", n, err)
	}
	order, _ := app.FindRecordById("orders", res.OrderID)
	if order.GetString("status") != "cancelled" {
		t.Errorf("status = %s, want cancelled", order.GetString("status"))
	}
	if r := variant(t, app, "TEE-LOGO-M").GetInt("reserved"); r != 0 {
		t.Errorf("reserved = %d, want 0", r)
	}
	// manual and already-handled orders are never touched
	if n, _ := shop.ReleaseExpired(app, time.Now().Add(24*time.Hour)); n != 0 {
		t.Errorf("second run released %d", n)
	}
}

func TestOrderNumbers(t *testing.T) {
	app := seededApp(t)

	// the seed's four orders are numbered from 1001 in the order created
	seeded, err := app.FindRecordsByFilter("orders", "number > 0", "number", 0, 0)
	if err != nil || len(seeded) != 4 || seeded[0].GetInt("number") != 1001 || seeded[3].GetInt("number") != 1004 {
		t.Fatalf("seeded order numbers: %d orders (%v)", len(seeded), err)
	}

	dev := variant(t, app, "CHEM-DEV-R1-1L")
	res, err := shop.Checkout(context.Background(), app, &fakePayments{}, shop.CheckoutRequest{
		Lines: []shop.CheckoutLine{{Variant: dev.Id, Qty: 1}}, Email: "a@example.com", Name: "A",
	})
	if err != nil || res.Number != 1005 {
		t.Fatalf("checkout number = %d (%v), want 1005", res.Number, err)
	}

	// an order typed into the admin UI gets the next number too
	admin := core.NewRecord(seeded[0].Collection())
	admin.Load(map[string]any{
		"storefront": seeded[0].GetString("storefront"), "channel": seeded[0].GetString("channel"),
		"user": seeded[0].GetString("user"), "email": "b@example.com", "status": "unconfirmed",
	})
	if err := app.Save(admin); err != nil || admin.GetInt("number") != 1006 {
		t.Fatalf("admin order number = %d (%v), want 1006", admin.GetInt("number"), err)
	}

	// numbers are unique
	dup := core.NewRecord(seeded[0].Collection())
	dup.Load(map[string]any{
		"storefront": seeded[0].GetString("storefront"), "channel": seeded[0].GetString("channel"),
		"user": seeded[0].GetString("user"), "email": "c@example.com", "status": "unconfirmed", "number": 1001,
	})
	if err := app.Save(dup); err == nil {
		t.Error("saved a duplicate order number")
	}

	if totals, err := app.FindRecordById("order_totals", res.OrderID); err != nil || totals.GetInt("number") != 1005 {
		t.Errorf("order_totals number = %v (%v)", totals, err)
	}
}
