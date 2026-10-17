# Build plan

Work through the steps in order. Each step ends with something Harry can try in a browser
and tests that pass with `go test ./...`. Stop after each step and show Harry what changed
before starting the next. Background and the full schema are in `background.md`.

## Repository layout (target)

```
main.go                 PocketBase app, static serving, hook and route wiring, cron
migrations/             Go migrations (collections, API rules, seed) + API rule tests
shop/                   Go package: Stripe checkout and webhook, community order, reprice, hooks
  *_test.go             tests using github.com/pocketbase/pocketbase/tests
site/                   Hugo project (config, layouts, content adapter, static/)
  static/js/api.js      tiny fetch wrapper: base URL, auth token, JSON errors
  static/vendor/        pico.min.css (v2.1.1), committed
pb_public/              Hugo build output (gitignored)
pb_data/                SQLite + uploads (gitignored)
Makefile                dev, site, test, build targets
.env.example            STRIPE_SECRET_KEY, STRIPE_WEBHOOK_SECRET, SITE_URL
```

## Step 1: Skeleton (done)
- Module, `main.go` with migratecmd (Go templates) and `pb_public` static serving, Makefile,
  `.gitignore`.
- Proof of concept: `clicks` collection (public list and create, no update/delete). The home page
  reads `totalItems` and POSTs a click with plain `fetch`. No custom Go routes.
- **Try it:** `make site && make dev`, open http://127.0.0.1:8090/ and click +1.

## Step 2: Catalog schema and seed
- Migration removing the `clicks` proof of concept (and the counter from the home page).
- Migrations for `channels`, `products` (with `slug`), `variants`, `prices` and the extra `users`
  fields, with the API rules from `background.md`.
- A seed migration (or `make seed`) that adds channels `shop` (stripe, public) and `community`
  (manual, requires_account), plus 2 prints and 1 t-shirt with S/M/L variants and shop prices.
- **Tests:** anonymous `GET /api/collections/prices/records` returns shop prices but not community
  ones; a signed-in user sees both; inactive products are hidden; anonymous writes to every
  catalog collection are refused.

## Step 3: Public site with Hugo
- `site/` content adapter `site/content/products/_content.gotmpl`: `resources.GetRemote` on
  `{{ site.Params.api }}/api/collections/products/records?filter=active=true&expand=...`
  (and variants/prices for the `shop` channel), `transform.Unmarshal`, then `.AddPage` per product
  with title, slug, description, images, variants and prices as page params.
  If expand can't reach variants/prices from products, fetch those collections separately and
  join in the template.
- Layouts: base (Pico CSS, `api.js`), home, product list grid, product page with variant
  picker and an add-to-cart button, about, shipping/returns policy stub.
- Product images: use PocketBase file URLs (`/api/files/products/{id}/{filename}?thumb=...`).
  Configure `thumbs` on the images field.
- `make site` = `hugo --source site` (PocketBase must be running for the content adapter).
- Design: plain, image-led grid; big photos; one accent color via Pico CSS variables; works on phones.
- **Try it:** `make site`, reload http://127.0.0.1:8090/, browse products.

## Step 4: Live stock and cart (JS + collection API only)
- Product pages fetch `/api/collections/variants/records/{id}` and show "In stock", "2 left" or
  "Sold out" from `on_hand - reserved`.
- Cart lives in `localStorage` as `[{variant, qty}]` (max ~20 lines). Header badge reads it.
- `site/content/cart.md`: JS fetches the cart's variants and shop prices from the API, renders
  lines and a subtotal, lets the buyer change quantities, and drops lines whose variant is gone or
  inactive. Displayed prices are for show only; checkout reprices on the server.
- No Go code in this step.
- **Try it:** add items, reload, change quantities, mark a product inactive in `/_/` and see it drop.

## Step 5: Checkout and webhook in a Stripe sandbox
- Migrations for `orders`, `order_lines`, `payments`, `stripe_events` with the rules in `background.md`.
- `POST /shop/checkout` (JSON `{lines:[{variant, qty}]}`): validate, reprice from the DB, reserve
  stock and create the `pending` order and lines in one transaction, create the Stripe session,
  return `{url}`. The cart page redirects to it. Out of stock returns 409 with the variant IDs;
  the cart page shows which lines and nothing is reserved.
- `POST /shop/stripe/webhook` and the 15-minute release cron, as described in `background.md`.
- Success page is a Hugo page `/order/?session_id=...` that reads the order through the API
  (view rule matches `stripe_session_id = @request.query.session_id`). Read only.
- Use `stripe.NewClient(key)` and `sc.V1CheckoutSessions.Create`. Hide it behind a small
  interface so checkout can be tested without the network.
- **Tests:**
  - Checkout: unknown, inactive or over-stock lines are rejected; prices come from the DB.
  - Reservation: two concurrent checkouts for the last unit, only one succeeds.
  - Webhook: build signed payloads with `webhook.GenerateTestSignedPayload` from stripe-go; check
    bad signature = 400, completed marks paid and moves stock, a duplicate event is a no-op,
    expired releases stock, refunded adds a negative payment.
  - Order view rule: right session id sees the order, wrong or missing one gets 404.
- **Try it:** `.env` with sandbox keys, `stripe listen --forward-to localhost:8090/shop/stripe/webhook`,
  buy with 4242 4242 4242 4242, watch the order go `pending` to `paid` and `on_hand` drop in `/_/`.

## Step 6: Expiry and refunds by hand
- **Try it:** start a checkout and abandon it (or `stripe trigger checkout.session.expired`);
  confirm `reserved` returns to 0 and the order is `cancelled`. Refund an order in the Stripe
  dashboard; confirm the negative `payments` row.

## Step 7: Accounts and Community
- Hugo pages `/login/`, `/signup/`, `/account/` using PocketBase's auth API
  (`auth-with-password`, `users` create, `request-password-reset`). `api.js` keeps the token in
  `localStorage` and sends it as `Authorization`. A record hook sets `consent_at` when
  `marketing_consent` is set.
- `/community/` Hugo page: JS lists community products and prices (the price rule already hides
  them from anonymous users).
- `POST /shop/community/order` (`apis.RequireAuth()`): creates an `awaiting_payment` order with
  lines at the current estimated price. No stock reserved.
- `/account/orders/` and `/account/order/?id=...`: the member's orders, lines and payments through
  the API; the page shows balance = total minus payments.
- Harry records Zelle/Venmo payments in `/_/` (`payments` with `external_ref`).
- `POST /shop/admin/reprice` behind `apis.RequireSuperuserAuth()`: set the real `unit_cents` on all
  open lines for a variant, recompute totals. Document the curl; a form page is optional.
- Hook: when a payment brings the balance to zero, set the order `paid`.
- **Tests:** one member can't see another's orders, lines or payments; community order uses DB
  prices and needs auth; reprice updates totals and balances; duplicate `external_ref` is rejected.

## Step 8: Deploy (do with Harry, not alone)
- `make build` for linux/amd64, systemd unit file in `deploy/`, env file template, backup and
  Litestream config in `deploy/`, a written restore test procedure. Turn on PocketBase's rate
  limiter for public create endpoints. No production changes without Harry asking.

## Before go-live (Harry's checklist, not code)
- Washington DOR registration; add WA registration in Stripe Tax; live keys and live webhook
  endpoint subscribed to the three events; test restore done; shipping and returns policy written.
