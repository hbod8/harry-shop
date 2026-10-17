package shop_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"
	"github.com/stripe/stripe-go/v87"
	"github.com/stripe/stripe-go/v87/webhook"

	"github.com/hbod8/harry-shop/shop"
)

const whsec = "whsec_test_secret"

var cfg = shop.Config{WebhookSecret: whsec, SiteURL: "http://127.0.0.1:8090", ShippingCents: 600}

// signed builds a Stripe event and signs it like Stripe does.
func signed(t testing.TB, id, typ string, object any) (payload []byte, header string) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"id": id, "object": "event", "api_version": stripe.APIVersion,
		"created": time.Now().Unix(), "type": typ,
		"data": map[string]any{"object": object},
	})
	if err != nil {
		t.Fatal(err)
	}
	return payload, webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: whsec}).Header
}

func send(t testing.TB, app core.App, id, typ string, object any) {
	t.Helper()
	payload, header := signed(t, id, typ, object)
	if err := shop.HandleStripeEvent(app, cfg, payload, header); err != nil {
		t.Fatalf("%s %s: %v", typ, id, err)
	}
}

// completedSession is what Stripe sends when the buyer has paid.
func completedSession(sessionID, orderID string, total int64) map[string]any {
	return map[string]any{
		"id": sessionID, "object": "checkout.session", "client_reference_id": orderID,
		"status": "complete", "payment_status": "paid", "payment_intent": "pi_" + sessionID,
		"amount_subtotal": total - 1000, "amount_total": total,
		"total_details":    map[string]any{"amount_shipping": 600, "amount_tax": 400, "amount_discount": 0},
		"customer_details": map[string]any{"email": "paid.buyer@example.com", "name": "Paid Buyer", "phone": "+12065550100"},
		"collected_information": map[string]any{"shipping_details": map[string]any{
			"name": "Paid Buyer",
			"address": map[string]any{"line1": "1 Pike St", "city": "Seattle", "state": "WA",
				"postal_code": "98101", "country": "US"},
		}},
	}
}

func linesOf(t testing.TB, app core.App, orderID string) map[string][]*core.Record {
	t.Helper()
	recs, err := app.FindRecordsByFilter("order_lines", "order = {:o}", "created", 0, 0, map[string]any{"o": orderID})
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[string][]*core.Record{}
	for _, r := range recs {
		byKind[r.GetString("kind")] = append(byKind[r.GetString("kind")], r)
	}
	return byKind
}

func totals(t testing.TB, app core.App, orderID string) (int, int, string) {
	t.Helper()
	r, err := app.FindRecordById("order_totals", orderID)
	if err != nil {
		t.Fatal(err)
	}
	return r.GetInt("total"), r.GetInt("balance"), r.GetString("payment_status")
}

// stock returns on_hand and reserved for a sku.
func stock(t testing.TB, app core.App, sku string) [2]int {
	v := variant(t, app, sku)
	return [2]int{v.GetInt("on_hand"), v.GetInt("reserved")}
}

func cardOrder(t testing.TB, app core.App, sku string, qty int) (orderID, sessionID string) {
	t.Helper()
	pay := &fakePayments{}
	res, err := shop.Checkout(context.Background(), app, pay, cardRequest(shop.CheckoutLine{Variant: variant(t, app, sku).Id, Qty: qty}))
	if err != nil {
		t.Fatal(err)
	}
	order, _ := app.FindRecordById("orders", res.OrderID)
	return res.OrderID, order.GetString("stripe_session_id")
}

func TestWebhookRejectsBadSignatures(t *testing.T) {
	app := seededApp(t)
	payload, header := signed(t, "evt_1", "checkout.session.completed", map[string]any{"id": "cs_x"})

	for name, h := range map[string]string{
		"missing":         "",
		"wrong secret":    webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: "whsec_other"}).Header,
		"tampered":        header,
		"stale timestamp": webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: payload, Secret: whsec, Timestamp: time.Now().Add(-time.Hour)}).Header,
	} {
		body := payload
		if name == "tampered" {
			body = []byte(strings.Replace(string(payload), "cs_x", "cs_y", 1))
		}
		if err := shop.HandleStripeEvent(app, cfg, body, h); !errors.Is(err, shop.ErrBadSignature) {
			t.Errorf("%s: err = %v, want ErrBadSignature", name, err)
		}
	}
	if n, _ := app.CountRecords("stripe_events"); n != 0 {
		t.Errorf("recorded %d events from bad payloads", n)
	}
}

func TestWebhookCompletedThenRefunded(t *testing.T) {
	app := seededApp(t)
	before := stock(t, app, "TEE-LOGO-M") // 6 on hand
	orderID, sessionID := cardOrder(t, app, "TEE-LOGO-M", 2)
	if got := stock(t, app, "TEE-LOGO-M"); got != [2]int{before[0], 2} {
		t.Fatalf("after checkout stock = %v", got)
	}

	// $56 items + $6 shipping + $4 tax
	send(t, app, "evt_paid", "checkout.session.completed", completedSession(sessionID, orderID, 6600))

	order, _ := app.FindRecordById("orders", orderID)
	if order.GetString("status") != "confirmed" || order.GetString("ship_city") != "Seattle" ||
		order.GetString("ship_postal") != "98101" || order.GetString("email") != "paid.buyer@example.com" ||
		order.GetString("stripe_payment_intent") != "pi_"+sessionID {
		t.Errorf("order = %v", order.PublicExport())
	}
	if got := stock(t, app, "TEE-LOGO-M"); got != [2]int{before[0] - 2, 0} {
		t.Errorf("stock = %v, want %d on hand, 0 reserved", got, before[0]-2)
	}
	lines := linesOf(t, app, orderID)
	if len(lines["shipping"]) != 1 || lines["shipping"][0].GetInt("unit_cents") != 600 ||
		len(lines["tax"]) != 1 || lines["tax"][0].GetInt("unit_cents") != 400 ||
		len(lines["payment"]) != 1 || lines["payment"][0].GetInt("unit_cents") != 6600 ||
		lines["payment"][0].GetString("external_ref") != "pi_"+sessionID {
		t.Errorf("lines = %v", lines)
	}
	if total, balance, status := totals(t, app, orderID); total != 6600 || balance != 0 || status != "paid" {
		t.Errorf("totals = %d %d %s", total, balance, status)
	}

	// Stripe retries: the same event again changes nothing
	send(t, app, "evt_paid", "checkout.session.completed", completedSession(sessionID, orderID, 6600))
	if n := len(linesOf(t, app, orderID)["payment"]); n != 1 {
		t.Errorf("duplicate event added payments: %d", n)
	}
	if got := stock(t, app, "TEE-LOGO-M"); got[0] != before[0]-2 {
		t.Errorf("duplicate event moved stock again: %v", got)
	}

	// partial refund, then the rest: charge.refunded carries the running total
	charge := func(refunded int64) map[string]any {
		return map[string]any{"id": "ch_1", "object": "charge", "amount": 6600,
			"amount_refunded": refunded, "refunded": refunded == 6600, "payment_intent": "pi_" + sessionID}
	}
	send(t, app, "evt_refund_1", "charge.refunded", charge(1000))
	if _, balance, status := totals(t, app, orderID); balance != 1000 || status != "partial" {
		t.Errorf("after partial refund: balance %d %s", balance, status)
	}
	send(t, app, "evt_refund_2", "charge.refunded", charge(6600))
	refunds := linesOf(t, app, orderID)["refund"]
	if len(refunds) != 2 || refunds[0].GetInt("unit_cents") != 1000 || refunds[1].GetInt("unit_cents") != 5600 {
		t.Errorf("refund lines = %v", refunds)
	}
	if _, _, status := totals(t, app, orderID); status != "refunded" {
		t.Errorf("after full refund: %s", status)
	}
}

func TestWebhookExpired(t *testing.T) {
	app := seededApp(t)
	before := stock(t, app, "TEE-LOGO-M")
	orderID, sessionID := cardOrder(t, app, "TEE-LOGO-M", 3)

	send(t, app, "evt_exp", "checkout.session.expired", map[string]any{
		"id": sessionID, "object": "checkout.session", "client_reference_id": orderID,
		"status": "expired", "payment_status": "unpaid",
	})
	order, _ := app.FindRecordById("orders", orderID)
	if order.GetString("status") != "cancelled" {
		t.Errorf("status = %s", order.GetString("status"))
	}
	if got := stock(t, app, "TEE-LOGO-M"); got != before {
		t.Errorf("stock = %v, want %v", got, before)
	}
}

func TestWebhookPaidAfterCancelled(t *testing.T) {
	app := seededApp(t)
	before := stock(t, app, "TEE-LOGO-M")
	orderID, sessionID := cardOrder(t, app, "TEE-LOGO-M", 1)
	if n, err := shop.ReleaseExpired(app, time.Now().Add(time.Hour)); err != nil || n != 1 {
		t.Fatalf("expired %d (%v)", n, err)
	}

	send(t, app, "evt_late", "checkout.session.completed", completedSession(sessionID, orderID, 3800))
	order, _ := app.FindRecordById("orders", orderID)
	if order.GetString("status") != "confirmed" {
		t.Errorf("status = %s, want confirmed", order.GetString("status"))
	}
	if got := stock(t, app, "TEE-LOGO-M"); got != [2]int{before[0] - 1, 0} {
		t.Errorf("stock = %v", got)
	}
}

func TestWebhookUnpaidAndUnknown(t *testing.T) {
	app := seededApp(t)
	orderID, sessionID := cardOrder(t, app, "TEE-LOGO-M", 1)

	unpaid := completedSession(sessionID, orderID, 3800)
	unpaid["payment_status"] = "unpaid" // e.g. a bank transfer still pending
	send(t, app, "evt_pending", "checkout.session.completed", unpaid)
	if order, _ := app.FindRecordById("orders", orderID); order.GetString("status") != "pending" {
		t.Errorf("unpaid session confirmed the order")
	}

	// sessions that aren't ours and unhandled event types are acknowledged
	send(t, app, "evt_other", "checkout.session.completed", completedSession("cs_not_ours", "", 1000))
	send(t, app, "evt_misc", "customer.created", map[string]any{"id": "cus_1", "object": "customer"})
}

func TestWebhookRoute(t *testing.T) {
	app := seededApp(t)
	payload, header := signed(t, "evt_route", "customer.created", map[string]any{"id": "cus_1", "object": "customer"})
	register := func(t testing.TB, _ *tests.TestApp, e *core.ServeEvent) { shop.Register(e, cfg, &fakePayments{}) }
	factory := func(testing.TB) *tests.TestApp { return app }

	for _, s := range []tests.ApiScenario{
		{
			Name: "signed event", Method: http.MethodPost, URL: "/shop/stripe/webhook",
			Body: strings.NewReader(string(payload)), Headers: map[string]string{"Stripe-Signature": header},
			ExpectedStatus: 200,
		},
		{
			Name: "unsigned event", Method: http.MethodPost, URL: "/shop/stripe/webhook",
			Body: strings.NewReader(string(payload)), ExpectedStatus: 400,
			ExpectedContent: []string{"Invalid signature."},
		},
		{
			Name: "checkout returns a CheckoutError as JSON", Method: http.MethodPost, URL: "/shop/checkout",
			Body:    strings.NewReader(`{"lines":[],"email":"a@example.com","name":"A"}`),
			Headers: map[string]string{"Content-Type": "application/json"}, ExpectedStatus: 400,
			ExpectedContent: []string{`"message":"Your cart is empty."`, `"status":400`},
		},
		{
			Name: "rejected lines come back with their variant ids", Method: http.MethodPost, URL: "/shop/checkout",
			Body:    strings.NewReader(`{"lines":[{"variant":"zzzzzzzzzzzzzzz","qty":1}],"email":"a@example.com","name":"A"}`),
			Headers: map[string]string{"Content-Type": "application/json"}, ExpectedStatus: 400,
			ExpectedContent: []string{`"variants":["zzzzzzzzzzzzzzz"]`},
		},
	} {
		s.BeforeTestFunc = register
		s.TestAppFactory = factory
		s.DisableTestAppCleanup = true
		s.Test(t)
	}
}
