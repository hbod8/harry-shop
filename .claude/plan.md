# Build plan

Work through the steps in order. Each step ends with something Harry can try in a browser
and tests that pass with `go test ./...`. Stop after each step and show Harry what changed
before starting the next. Background and the full schema are in `background.md`.

## Repository layout (target)

```
main.go                 PocketBase app, route wiring, cron
migrations/             Go migrations (migratecmd, TemplateLangGo)
shop/                   Go package: checkout, webhook, cart, auth cookie, community, admin
  *_test.go             tests using github.com/pocketbase/pocketbase/tests
templates/              Go html/template files (layout, fragments, account pages)
site/                   Hugo project (config, layouts, content adapter, static/)
  static/vendor/        htmx.min.js (v4.0.0), pico.min.css (v2.1.1), committed
pb_public/              Hugo build output (gitignored)
pb_data/                SQLite + uploads (gitignored)
Makefile                dev, site, test, build targets
.env.example            STRIPE_SECRET_KEY, STRIPE_WEBHOOK_SECRET, SITE_URL
```

## Step 1: Skeleton
- `go mod init github.com/hbod8/harry-shop`; require `github.com/pocketbase/pocketbase v0.40.5`
  exactly (Go 1.27 toolchain).
- `main.go`: `pocketbase.New()`, `migratecmd.MustRegister` with `TemplateLang: migratecmd.TemplateLangGo`,
  `Automigrate` only when `osutils.IsProbablyGoRun()`, static `GET /{path...}` from `./pb_public`
  (copy the pattern in PocketBase's `examples/base/main.go`).
- `.gitignore`: `pb_data/`, `pb_public/`, `.env`, the built binary.
- Makefile: `make dev` = `go run . serve`; `make test` = `go test ./...`.
- **Try it:** `make dev`, open http://127.0.0.1:8090/_/ and create the superuser.

## Step 2: Catalog schema and seed
- Migrations for `channels`, `products` (with `slug`), `variants`, `prices` and the extra `users`
  fields, with the API rules from `background.md`.
- A seed migration (or `make seed`) that adds channels `shop` (stripe, public) and `community`
  (manual, requires_account), plus 2 prints and 1 t-shirt with S/M/L variants and shop prices.
- **Tests:** anonymous `GET /api/collections/prices/records` returns shop prices but not community
  ones; a signed-in user sees both; inactive products are hidden.

## Step 3: Public site with Hugo
- `site/` Hugo project, output dir `../pb_public`.
- Content adapter `site/content/products/_content.gotmpl`: `resources.GetRemote` on
  `{{ site.Params.api }}/api/collections/products/records?filter=active=true&expand=...`
  (and variants/prices for the `shop` channel), `transform.Unmarshal`, then `.AddPage` per product
  with title, slug, description, images, variants and prices as page params.
  If expand can't reach variants/prices from products, fetch those collections separately and
  join in the template.
- Layouts: base (Pico CSS, htmx script tag), home, product list grid, product page with variant
  picker and an add-to-cart form, about, shipping/returns policy stub.
- Product images: use PocketBase file URLs (`/api/files/products/{id}/{filename}?thumb=...`).
  Configure `thumbs` on the images field.
- Make target `make site` = `hugo --source site` (PocketBase must be running).
- Design: plain, image-led grid; big photos; one accent color via Pico CSS variables; works on phones.
- **Try it:** `make site`, reload http://127.0.0.1:8090/, browse products.

## Step 4: Live stock and cart (htmx)
- `GET /shop/stock/{variantId}` returns a small fragment ("In stock", "2 left", "Sold out").
  Product pages load it with `hx-get` on load.
- Cart in the unsigned `cart` cookie (see background). Routes, each returning a fragment:
  `POST /shop/cart/add`, `POST /shop/cart/update`, `GET /shop/cart` (cart body with server-side
  prices and a subtotal), `GET /shop/cart/count` (header badge).
- `site/content/cart.md` is a Hugo page whose body loads `/shop/cart` with htmx. Its checkout
  form is a plain `<form method="post" action="/shop/checkout">` (no JS needed).
- Forms must still work without JS where cheap (add-to-cart can fall back to a normal POST that
  redirects to `/cart/`).
- **Tests:** cart cookie with unknown IDs, inactive products, or qty over stock is cleaned up or
  rejected; prices come from the DB, not the cookie.

## Step 5: Checkout and webhook in a Stripe sandbox
- Migrations for `orders`, `order_lines`, `payments`, `stripe_events`.
- `POST /shop/checkout`, `POST /shop/stripe/webhook`, `GET /shop/orders/{id}` (success page,
  read-only), and the 15-minute release cron, as described in `background.md`.
- Use `stripe.NewClient(key)` and `sc.V1CheckoutSessions.Create`.
- Out-of-stock at checkout redirects back to `/cart/` with a message; nothing is reserved.
- **Tests:**
  - Reservation: two concurrent checkouts for the last unit, only one succeeds.
  - Webhook: build signed payloads with `webhook.GenerateTestSignedPayload` from stripe-go; check
    bad signature = 400, completed marks paid and moves stock, a duplicate event is a no-op,
    expired releases stock, refunded adds a negative payment.
  - Hide the Stripe call behind a small interface so checkout can be tested without the network.
- **Try it:** `.env` with sandbox keys, `stripe listen --forward-to localhost:8090/shop/stripe/webhook`,
  buy with 4242 4242 4242 4242, watch the order go `pending` to `paid` and `on_hand` drop in `/_/`.

## Step 6: Expiry and refunds by hand
- **Try it:** start a checkout and abandon it (or `stripe trigger checkout.session.expired`);
  confirm `reserved` returns to 0 and the order is `cancelled`. Refund an order in the Stripe
  dashboard; confirm the negative `payments` row.

## Step 7: Accounts and Community
- Cookie auth bridge, `/shop/login`, `/shop/logout`, `/shop/signup` (Go templates), Origin check
  on POSTs. Signup records `marketing_consent` and `consent_at`.
- `/community/` (Go template, sign-in required): community-channel products and prices, order form
  that creates an `awaiting_payment` order with lines at the estimated price. No stock reserved.
- `/account/orders` and `/account/orders/{id}`: the member's orders, lines, payments and balance.
- Harry records Zelle/Venmo payments in `/_/` (`payments` with `external_ref`).
- `POST /shop/admin/reprice` behind `apis.RequireSuperuserAuth()`: set the real `unit_cents` on all
  open lines for a variant, recompute totals. A minimal admin form page is fine, or document a curl.
- Hook: when a payment brings the balance to zero, set the order `paid`.
- **Tests:** one member can't see another's orders; reprice updates totals and balances; duplicate
  `external_ref` is rejected; CSRF Origin check blocks a cross-site POST.

## Step 8: Deploy (do with Harry, not alone)
- `make build` for linux/amd64, systemd unit file in `deploy/`, env file template, backup and
  Litestream config in `deploy/`, a written restore test procedure. No production changes
  without Harry asking.

## Before go-live (Harry's checklist, not code)
- Washington DOR registration; add WA registration in Stripe Tax; live keys and live webhook
  endpoint subscribed to the three events; test restore done; shipping and returns policy written.
