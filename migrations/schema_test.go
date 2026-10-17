package migrations_test

import (
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	_ "github.com/hbod8/harry-shop/migrations"
	"github.com/hbod8/harry-shop/shop"
)

const api = "/api/collections/"

// seededApp is a fresh database built only from our migrations plus the
// dev sample data.
func seededApp(t testing.TB) *tests.TestApp {
	app, err := tests.NewTestApp(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := shop.Seed(app); err != nil {
		t.Fatal(err)
	}
	return app
}

// seedIDs holds the generated ids of the sample records, found by their
// unique keys.
type seedIDs struct {
	pike, rainier, tee, developer, retired string // products
	devVariant                             string
	sam, guest                             string // users
	groupOrder, guestOrder                 string
}

func lookup(t testing.TB, app core.App) seedIDs {
	t.Helper()
	find := func(collection, field, value string) string {
		rec, err := app.FindFirstRecordByData(collection, field, value)
		if err != nil {
			t.Fatalf("%s %s=%q: %v", collection, field, value, err)
		}
		return rec.Id
	}
	id := seedIDs{
		pike:       find("products", "slug", "pike-place-at-dawn"),
		rainier:    find("products", "slug", "rainier-from-kerry-park"),
		tee:        find("products", "slug", "harry-logo-tee"),
		developer:  find("products", "slug", "developer-round-1"),
		retired:    find("products", "slug", "retired-print"),
		devVariant: find("variants", "sku", "CHEM-DEV-R1-1L"),
		sam:        find("users", "email", "sam@example.com"),
		guest:      find("users", "email", "guest@example.com"),
		guestOrder: find("orders", "stripe_session_id", shop.SeedGuestSession),
	}
	zelle, err := app.FindFirstRecordByData("order_lines", "external_ref", "ZELLE-1001")
	if err != nil {
		t.Fatal(err)
	}
	id.groupOrder = zelle.GetString("order")
	return id
}

// check is one API request and what it must (not) return.
type check struct {
	name    string
	method  string
	url     string
	user    string // users id to authenticate as; empty = anonymous
	body    string
	status  int
	want    []string
	notWant []string
}

// runAll runs the checks against one shared app; none of them may write.
func runAll(t *testing.T, app *tests.TestApp, checks []check) {
	t.Helper()
	for _, c := range checks {
		method := c.method
		if method == "" {
			method = http.MethodGet
		}
		s := tests.ApiScenario{
			Name:                  c.name,
			Method:                method,
			URL:                   c.url,
			Headers:               map[string]string{},
			ExpectedStatus:        c.status,
			ExpectedContent:       c.want,
			NotExpectedContent:    c.notWant,
			TestAppFactory:        func(testing.TB) *tests.TestApp { return app },
			DisableTestAppCleanup: true,
		}
		if c.body != "" {
			s.Body = strings.NewReader(c.body)
			s.Headers["Content-Type"] = "application/json"
		}
		if c.user != "" {
			rec, err := app.FindRecordById("users", c.user)
			if err != nil {
				t.Fatal(err)
			}
			token, err := rec.NewAuthToken()
			if err != nil {
				t.Fatal(err)
			}
			s.Headers["Authorization"] = token
		}
		s.Test(t)
	}
}

var forbidden = []string{`"status":403`}

func TestCatalogRules(t *testing.T) {
	app := seededApp(t)
	defer app.Cleanup()
	id := lookup(t, app)

	runAll(t, app, []check{
		{
			name:    "product list hides unlisted and inactive",
			url:     api + "products/records",
			status:  200,
			want:    []string{id.pike, id.rainier, id.tee},
			notWant: []string{id.developer, id.retired},
		},
		{
			name:   "filtering can't reveal unlisted products",
			url:    api + "products/records?filter=listed=false",
			status: 200,
			want:   []string{`"totalItems":0`},
		},
		{
			name:   "unlisted product is viewable by id",
			url:    api + "products/records/" + id.developer,
			status: 200,
			want:   []string{id.developer},
		},
		{
			name:   "unlisted product expands its variants",
			url:    api + "products/records/" + id.developer + "?expand=variants_via_product",
			status: 200,
			want:   []string{"CHEM-DEV-R1-1L"},
		},
		{
			name:   "inactive product is not viewable",
			url:    api + "products/records/" + id.retired,
			status: 404,
			want:   []string{`"status":404`},
		},
		{
			name:    "variant list hides unlisted and inactive products' variants",
			url:     api + "variants/records?perPage=100",
			status:  200,
			want:    []string{"TEE-LOGO-M", "PRINT-PIKE-8X10"},
			notWant: []string{"CHEM-DEV-R1-1L", "PRINT-RETIRED-8X10"},
		},
		{
			name:   "unlisted variant is viewable by id",
			url:    api + "variants/records/" + id.devVariant,
			status: 200,
			want:   []string{"CHEM-DEV-R1-1L"},
		},
		{
			name:   "storefronts, channels and terms are public",
			url:    api + "terms/records",
			status: 200,
			want:   []string{"seattle-rain", "holiday-2026"},
		},
		{name: "anonymous can't create products", method: http.MethodPost, url: api + "products/records", body: `{}`, status: 403, want: forbidden},
		{name: "anonymous can't change stock", method: http.MethodPatch, url: api + "variants/records/" + id.devVariant, body: `{"on_hand":999}`, status: 403, want: forbidden},
		{name: "users can't create terms", method: http.MethodPost, url: api + "terms/records", user: id.sam, body: `{}`, status: 403, want: forbidden},
		{name: "anonymous can't delete channels", method: http.MethodDelete, url: api + "channels/records/x", status: 403, want: forbidden},
		{name: "anonymous can't edit storefronts", method: http.MethodPatch, url: api + "storefronts/records/x", body: `{}`, status: 403, want: forbidden},
	})
}

func TestOrderRules(t *testing.T) {
	app := seededApp(t)
	defer app.Cleanup()
	id := lookup(t, app)

	runAll(t, app, []check{
		{
			name:   "anonymous sees no orders",
			url:    api + "orders/records",
			status: 200,
			want:   []string{`"totalItems":0`},
		},
		{
			name:    "a user lists only their own orders",
			url:     api + "orders/records",
			user:    id.sam,
			status:  200,
			want:    []string{id.groupOrder},
			notWant: []string{id.guestOrder},
		},
		{
			name:   "a user can't view someone else's order",
			url:    api + "orders/records/" + id.groupOrder,
			user:   id.guest,
			status: 404,
			want:   []string{`"status":404`},
		},
		{
			name:   "matching session id shows a guest order",
			url:    api + "orders/records/" + id.guestOrder + "?session_id=" + shop.SeedGuestSession,
			status: 200,
			want:   []string{id.guestOrder},
		},
		{
			name:   "wrong session id shows nothing",
			url:    api + "orders/records/" + id.guestOrder + "?session_id=cs_test_wrong",
			status: 404,
			want:   []string{`"status":404`},
		},
		{
			name:   "blank session id doesn't match orders without one",
			url:    api + "orders/records?session_id=",
			status: 200,
			want:   []string{`"totalItems":0`},
		},
		{
			name:    "session id shows that order's lines only",
			url:     api + "order_lines/records?session_id=" + shop.SeedGuestSession,
			status:  200,
			want:    []string{"PRINT-PIKE-8X10"},
			notWant: []string{"CHEM-DEV-R1-1L"},
		},
		{
			name:    "a user lists only their own order lines",
			url:     api + "order_lines/records",
			user:    id.sam,
			status:  200,
			want:    []string{"CHEM-DEV-R1-1L"},
			notWant: []string{"PRINT-PIKE-8X10"},
		},
		{
			name:   "a user sees their own payment lines",
			url:    api + "order_lines/records?filter=kind='payment'",
			user:   id.sam,
			status: 200,
			want:   []string{"ZELLE-1001"},
		},
		{
			name:    "another user sees none of them",
			url:     api + "order_lines/records",
			user:    id.guest,
			status:  200,
			notWant: []string{"ZELLE-1001", "CHEM-DEV-R1-1L"},
		},
		{
			name:    "a user sees only their own order totals",
			url:     api + "order_totals/records",
			user:    id.sam,
			status:  200,
			want:    []string{id.groupOrder, `"total":3600`, `"paid":2000`, `"balance":1600`, `"payment_status":"partial"`},
			notWant: []string{id.guestOrder},
		},
		{
			name:   "session id shows a guest order's totals",
			url:    api + "order_totals/records/" + id.guestOrder + "?session_id=" + shop.SeedGuestSession,
			status: 200,
			want:   []string{`"total":4100`, `"balance":4100`, `"payment_status":"unpaid"`},
		},
		{
			name:   "anonymous sees no order totals",
			url:    api + "order_totals/records?session_id=",
			status: 200,
			want:   []string{`"totalItems":0`},
		},
		{
			name:    "a user sees only their own user record",
			url:     api + "users/records",
			user:    id.sam,
			status:  200,
			want:    []string{id.sam},
			notWant: []string{id.guest},
		},
		{name: "users can't create orders directly", method: http.MethodPost, url: api + "orders/records", user: id.sam, body: `{}`, status: 403, want: forbidden},
		{name: "users can't edit their order", method: http.MethodPatch, url: api + "orders/records/" + id.groupOrder, user: id.sam, body: `{"status":"fulfilled"}`, status: 403, want: forbidden},
		{name: "users can't add order lines", method: http.MethodPost, url: api + "order_lines/records", user: id.sam, body: `{}`, status: 403, want: forbidden},
		{name: "users can't read notes about themselves", url: api + "user_notes/records", user: id.sam, status: 403, want: forbidden},
		{name: "stripe events are locked", url: api + "stripe_events/records", status: 403, want: forbidden},
	})
}

func TestConstraints(t *testing.T) {
	app := seededApp(t)
	defer app.Cleanup()
	id := lookup(t, app)

	save := func(collection string, data map[string]any) error {
		c, err := app.FindCollectionByNameOrId(collection)
		if err != nil {
			t.Fatal(err)
		}
		rec := core.NewRecord(c)
		rec.Load(data)
		return app.Save(rec)
	}
	mustFail := func(name string, err error) {
		t.Helper()
		if err == nil {
			t.Errorf("%s: saved, want error", name)
		}
	}
	mustPass := func(name string, err error) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	manual, _ := app.FindFirstRecordByData("channels", "slug", "manual")
	harry, _ := app.FindFirstRecordByData("storefronts", "slug", "harry")
	order := func(session string) map[string]any {
		return map[string]any{
			"storefront": harry.Id, "channel": manual.Id, "user": id.sam,
			"email": "sam@example.com", "status": "unconfirmed",
			"stripe_session_id": session,
		}
	}

	mustFail("duplicate sku", save("variants", map[string]any{"product": id.tee, "sku": "TEE-LOGO-M", "name": "M again"}))
	mustFail("duplicate term in a taxonomy", save("terms", map[string]any{"taxonomy": "tags", "slug": "seattle-rain", "name": "x"}))
	mustPass("same term slug in another taxonomy", save("terms", map[string]any{"taxonomy": "collections", "slug": "seattle-rain", "name": "x"}))
	mustFail("bad slug", save("terms", map[string]any{"taxonomy": "tags", "slug": "Seattle Rain", "name": "x"}))
	mustFail("unknown taxonomy", save("terms", map[string]any{"taxonomy": "themes", "slug": "x", "name": "x"}))
	mustFail("duplicate product slug in a storefront", save("products", map[string]any{
		"storefront": harry.Id, "product_type": "print", "title": "x", "slug": "pike-place-at-dawn", "channels": []string{manual.Id},
	}))
	mustFail("product without a channel", save("products", map[string]any{
		"storefront": harry.Id, "product_type": "print", "title": "x", "slug": "no-channel",
	}))
	mustFail("unknown product type", save("products", map[string]any{
		"storefront": harry.Id, "product_type": "accessory", "title": "x", "slug": "bad-type", "channels": []string{manual.Id},
	}))
	mustFail("negative stock", save("variants", map[string]any{"product": id.tee, "sku": "TEE-NEG", "name": "x", "on_hand": -1}))
	mustFail("fractional cents", save("variants", map[string]any{"product": id.tee, "sku": "TEE-FRAC", "name": "x", "price_cents": 10.5}))

	mustPass("blank session ids may repeat", save("orders", order("")))
	mustFail("duplicate session id", save("orders", order(shop.SeedGuestSession)))

	payment := func(ref string, quantity int) map[string]any {
		return map[string]any{
			"order": id.groupOrder, "kind": "payment", "name": "cash", "channel": manual.Id,
			"quantity": quantity, "unit_cents": 500, "external_ref": ref,
		}
	}
	mustPass("cash without a reference", save("order_lines", payment("", 1)))
	mustPass("another cash without a reference", save("order_lines", payment("", 1)))
	mustFail("duplicate external reference", save("order_lines", payment("ZELLE-1001", 1)))
	mustFail("zero quantity", save("order_lines", payment("", 0)))
	mustFail("unknown line kind", save("order_lines", map[string]any{
		"order": id.groupOrder, "kind": "discount", "name": "x", "quantity": 1,
	}))
	mustFail("negative amount", save("order_lines", map[string]any{
		"order": id.groupOrder, "kind": "item", "name": "x", "quantity": 1, "unit_cents": -100,
	}))

	rec, err := app.FindRecordById("orders", id.groupOrder)
	if err != nil {
		t.Fatal(err)
	}
	mustFail("deleting an order that has lines", app.Delete(rec))

	if err := shop.Seed(app); !errors.Is(err, shop.ErrAlreadySeeded) {
		t.Errorf("second seed: %v, want ErrAlreadySeeded", err)
	}
}

func TestOrderTotals(t *testing.T) {
	type line struct {
		kind            string
		quantity, cents int
	}
	items := []line{{"item", 2, 1800}, {"shipping", 1, 600}, {"tax", 1, 400}} // total 4600

	cases := []struct {
		name          string
		lines         []line
		paid, balance int
		status        string
	}{
		{"no lines", nil, 0, 0, "unpaid"},
		{"nothing paid", items, 0, 4600, "unpaid"},
		{"part paid", append(items, line{"payment", 1, 2000}), 2000, 2600, "partial"},
		{"paid in two payments", append(items, line{"payment", 1, 2000}, line{"payment", 1, 2600}), 4600, 0, "paid"},
		{"overpaid", append(items, line{"payment", 1, 5000}), 5000, -400, "overpaid"},
		{"partly refunded", append(items, line{"payment", 1, 4600}, line{"refund", 1, 1000}), 3600, 1000, "partial"},
		{"fully refunded", append(items, line{"payment", 1, 4600}, line{"refund", 1, 4600}), 0, 4600, "refunded"},
	}

	app := seededApp(t)
	defer app.Cleanup()
	id := lookup(t, app)
	manual, _ := app.FindFirstRecordByData("channels", "slug", "manual")
	harry, _ := app.FindFirstRecordByData("storefronts", "slug", "harry")
	ordersC, _ := app.FindCollectionByNameOrId("orders")
	linesC, _ := app.FindCollectionByNameOrId("order_lines")

	for _, c := range cases {
		order := core.NewRecord(ordersC)
		order.Load(map[string]any{
			"storefront": harry.Id, "channel": manual.Id, "user": id.sam,
			"email": "sam@example.com", "status": "unconfirmed",
		})
		if err := app.Save(order); err != nil {
			t.Fatal(err)
		}
		for _, l := range c.lines {
			rec := core.NewRecord(linesC)
			rec.Load(map[string]any{
				"order": order.Id, "kind": l.kind, "name": l.kind,
				"quantity": l.quantity, "unit_cents": l.cents,
			})
			if err := app.Save(rec); err != nil {
				t.Fatal(err)
			}
		}

		got, err := app.FindRecordById("order_totals", order.Id)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got.GetInt("paid") != c.paid || got.GetInt("balance") != c.balance || got.GetString("payment_status") != c.status {
			t.Errorf("%s: paid %d balance %d status %q, want %d %d %q", c.name,
				got.GetInt("paid"), got.GetInt("balance"), got.GetString("payment_status"),
				c.paid, c.balance, c.status)
		}
	}
}

func TestAdminViews(t *testing.T) {
	app := seededApp(t)
	defer app.Cleanup()
	id := lookup(t, app)

	runAll(t, app, []check{
		{name: "customers view is locked", url: api + "customers/records", user: id.sam, status: 403, want: forbidden},
		{name: "unconfirmed view is locked", url: api + "orders_unconfirmed/records", status: 403, want: forbidden},
		{name: "confirmed view is locked", url: api + "orders_confirmed/records", user: id.sam, status: 403, want: forbidden},
	})

	sam, err := app.FindRecordById("customers", id.sam)
	if err != nil {
		t.Fatal(err)
	}
	notes := sam.GetString("notes")
	if sam.GetInt("note_count") != 2 || !strings.Contains(notes, "[discord] Asked to pick up") ||
		!strings.Contains(notes, "[in_person] Met at the October photo walk.") {
		t.Errorf("sam: %d notes %q", sam.GetInt("note_count"), notes)
	}
	guest, err := app.FindRecordById("customers", id.guest)
	if err != nil {
		t.Fatal(err)
	}
	if guest.GetInt("note_count") != 0 || guest.GetString("notes") != "" {
		t.Errorf("guest: %d notes %q", guest.GetInt("note_count"), guest.GetString("notes"))
	}

	statusOf := func(view string) map[string]string {
		recs, err := app.FindAllRecords(view)
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, r := range recs {
			got[r.Id] = r.GetString("items") + " | " + r.GetString("payment_status")
		}
		return got
	}
	unconfirmed := statusOf("orders_unconfirmed")
	if len(unconfirmed) != 2 ||
		unconfirmed[id.groupOrder] != "2× Developer, round 1, Nov 2026 — 1 L | partial" ||
		unconfirmed[id.guestOrder] != "1× Pike Place at Dawn — 8×10 | unpaid" {
		t.Errorf("orders_unconfirmed = %v", unconfirmed)
	}
	confirmed := statusOf("orders_confirmed")
	if len(confirmed) != 1 {
		t.Errorf("orders_confirmed = %v, want only the confirmed tee", confirmed)
	}
	for _, v := range confirmed {
		if v != "1× Harry Logo Tee — M | paid" {
			t.Errorf("orders_confirmed row = %q", v)
		}
	}
}

func TestStats(t *testing.T) {
	app := seededApp(t)
	defer app.Cleanup()

	runAll(t, app, []check{
		{name: "stats are locked", url: api + "stats/records", status: 403, want: forbidden},
	})

	// Move the guest's fulfilled order to 2025, so the guest becomes a
	// returning customer and that order falls outside every period.
	old, err := app.FindFirstRecordByData("order_lines", "external_ref", "pi_test_seed_fulfilled")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE orders SET created = '2025-01-15 12:00:00.000Z' WHERE id = {:order}`,
		`UPDATE order_lines SET created = '2025-01-15 12:00:00.000Z' WHERE "order" = {:order}`,
	} {
		if _, err := app.DB().NewQuery(q).Bind(map[string]any{"order": old.GetString("order")}).Execute(); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := app.FindAllRecords("stats")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"last_7_days", "last_30_days", "this_month", "this_quarter", "this_year"}
	if len(rows) != len(want) {
		t.Fatalf("got %d periods, want %d", len(rows), len(want))
	}
	for _, r := range rows {
		// in every period: 3 recent orders; revenue is only the confirmed $28 tee
		// (unconfirmed orders and the guest's $6 shipping don't count); Sam new,
		// guest returning
		got := [4]int{r.GetInt("orders"), r.GetInt("revenue"), r.GetInt("new_customers"), r.GetInt("returning_customers")}
		if got != [4]int{3, 2800, 1, 1} {
			t.Errorf("%s: orders, revenue, new, returning = %v, want [3 2800 1 1]", r.Id, got)
		}
	}
}
