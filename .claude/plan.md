# Build plan

Work through the steps in order. Each step ends with something Harry can try in a browser
and tests that pass with `go test ./...`. Stop after each step and show Harry what changed
before starting the next. Background and the full schema are in `background.md`.

## Repository layout (target)

```
main.go                 PocketBase app, static serving, route wiring, cron, seed command
migrations/             Go migrations (collections, API rules, base config) + API rule tests
shop/                   Go package: checkout, Stripe webhook, expiry cron, consent hook, dev seed data
  *_test.go             tests using github.com/pocketbase/pocketbase/tests
site/                   Hugo project (config, layouts, content adapter, static/)
  assets/js/, css/      site JS and CSS, fingerprinted by Hugo so browsers never cache stale copies
  static/vendor/        pico.min.css (v2.1.1), committed
pb_public/              Hugo build output (gitignored)
pb_data/                SQLite + uploads (gitignored)
Makefile                dev, seed, site, test, build targets
.env.example            STRIPE_SECRET_KEY, STRIPE_WEBHOOK_SECRET, SITE_URL
```

## Step 1: Skeleton (done)
- Module, `main.go` with migratecmd (Go templates) and `pb_public` static serving, Makefile,
  `.gitignore`.
- Proof of concept (a `clicks` counter over the collection API, since removed) confirmed the
  API-first approach.

## Step 2: All collections and rules (Harry reviews in `/_/` before any Hugo work)
- Migrations for every collection in `background.md`: `storefronts`, `channels`, `terms`,
  `products`, `variants`, the extra `users` fields (and OTP), `user_notes`, `orders`,
  `order_lines` (the ledger), `stripe_events`, the `order_totals` view, and admin views
  `customers`, `orders_unconfirmed`, `orders_confirmed`, `stats`, with fields, indexes and API
  rules.
- Base config migration: storefront `harry`, channels `card` (stripe) and `manual`.
- `make seed` (dev only, not a migration): sample terms, 2 prints, 1 t-shirt (S/M/L), 1 unlisted
  chemistry item (manual only), a group-buy order with a Zelle payment line, a guest card order.
- **Tests:** anonymous lists hide unlisted and inactive products and their variants, while view
  by id still works for unlisted; anonymous writes to catalog collections are refused; a user
  sees only their own orders, lines and totals; `?session_id=` shows only the matching order and
  a blank one matches nothing; `user_notes` and `stripe_events` are locked; unique indexes (sku,
  term per taxonomy, external_ref when set, stripe_session_id when set); `order_totals` for
  unpaid, partial, paid, overpaid, partly and fully refunded orders.
- **Try it:** `make dev`, `make seed`, browse every collection in http://127.0.0.1:8090/_/ and try
  the API rules with curl.

## Step 3: Consent hook
- `users`: set `consent_at` when `marketing_consent` turns on, clear it when it turns off. This is
  the only hook; everything else admin-side is Harry in `/_/` or a view.
- **Tests:** sign-up with and without consent, turning it off later.

## Step 4: Public site with Hugo (done, pending Harry's review)
- `site/` content adapter `site/content/products/_content.gotmpl`: `resources.GetRemote` on
  `{{ site.Params.api }}/api/collections/products/records?filter=storefront.slug="harry"&expand=terms,variants_via_product`
  (listed products only, by the rules), `transform.Unmarshal`, then `.AddPage` per product with
  title, slug, plain-text description, images, variants, terms and `term_keys` as page params.
- `site/content/_content.gotmpl` builds taxonomy pages (`/tags/`) and term pages
  (`/tags/seattle-rain/`) from the `terms` API plus product types; no Hugo taxonomy config.
  Storefront slug is a site param, so another storefront is another build.
- Layouts: base (Pico CSS, `api.js`), home, product grid, product page with variant picker and an
  add-to-cart button, taxonomy and term pages, generic `/item/` page for unlisted products, about,
  shipping/returns policy stub.
- Product images: PocketBase file URLs (`/api/files/products/{id}/{filename}?thumb=...`).
- `make site` = `hugo --source site` (PocketBase must be running for the content adapter).
- Design: plain, image-led grid; big photos; one accent color via Pico CSS variables; works on phones.
- **Try it:** `make site`, browse products, tags and an unlisted item link.

## Step 5: Live stock and cart (JS + collection API only)
- Product pages fetch the variant and show "In stock", "2 left" or "Sold out" from
  `on_hand - reserved` (computed in the page).
- Cart lives in `localStorage` as `[{variant, qty}]` (max ~20 lines). Header badge reads it.
- `site/content/cart.md`: JS fetches the cart's variants from the API, renders lines and a
  subtotal, lets the buyer change quantities, drops lines that are gone or inactive, and shows
  which channels (card, manual) the whole cart allows. Displayed prices are for show only.
- **Try it:** add items, reload, change quantities, deactivate a product in `/_/` and see it drop.

## Step 6: Checkout and webhook in a Stripe sandbox
- `POST /shop/checkout` (JSON `{lines:[{variant, qty}], channel, email, name}`): validate lines,
  check every product allows the channel (else 400 "order these separately"), reprice from the DB,
  find or create the user by email, then in one transaction create the `unconfirmed` order and
  its `item` lines with `quoted_cents = unit_cents = price_cents` (plus a `shipping` line for
  shipped card orders).
  - `card`: reserve stock, create the Stripe session, return `{url}`. Out of stock = 409 with the
    variant ids, nothing reserved.
  - `manual`: no reservation, return the order page URL.
- `POST /shop/stripe/webhook` and the 15-minute release cron, as described in `background.md`.
- Success page `/order/?session_id=...` reads the order through the API. Read only.
- Use `stripe.NewClient(key)` and `sc.V1CheckoutSessions.Create`, behind a small interface so
  checkout can be tested without the network. Flat-rate `shipping_options`.
- **Tests:**
  - Checkout: unknown, inactive or over-stock lines rejected; prices from the DB; channel mismatch
    rejected; existing email reuses the user.
  - Reservation: two concurrent checkouts for the last unit, only one succeeds.
  - Webhook: signed payloads via `webhook.GenerateTestSignedPayload`; bad signature = 400,
    completed confirms the order, moves stock and adds tax and payment lines, a duplicate event
    is a no-op, expired releases stock and cancels, refunded adds a refund line.
- **Try it:** `.env` with sandbox keys, `stripe listen --forward-to localhost:8090/shop/stripe/webhook`,
  buy with 4242 4242 4242 4242, watch the order go `unconfirmed` to `confirmed` and its
  `order_totals` row to `paid`.

## Step 7: Expiry and refunds by hand
- **Try it:** abandon a checkout (or `stripe trigger checkout.session.expired`); confirm `reserved`
  returns to 0 and the order is `cancelled`. Refund in the Stripe dashboard; confirm the `refund`
  line and `order_totals.payment_status = refunded`.

## Step 8: Accounts and group buys
- Hugo pages `/login/` (email OTP, or password), `/account/` and `/account/order/?id=...` using
  PocketBase's auth API. `api.js` keeps the token in `localStorage`. The order page shows the
  lines (quoted vs final price, payments) and the `order_totals` row.
- No admin code: Harry records Zelle/Venmo/cash as `payment` lines and reprices by editing
  `unit_cents` on `unconfirmed` item lines, both in `/_/`.
- **Tests:** OTP sign-in sees the orders placed with that email.

## Step 9: Deploy (do with Harry, not alone)
- `make build` for linux/amd64, systemd unit file in `deploy/`, env file template, backup and
  Litestream config in `deploy/`, a written restore test procedure. SMTP settings for OTP mail. `TZ=America/Los_Angeles` in the unit so the
  `stats` view's calendar periods start at local midnight.
  Turn on PocketBase's rate limiter for public create endpoints. No production changes without
  Harry asking.

## Later (space left in the schema, not built)
- Views for outstanding orders, revenue and stats (Harry, via migrations).
- More storefronts: another Hugo build with a different storefront param; serve by `domain`.
- In-person booth: new channel providers (square, cash) by migration; shared stock already works.
- Per-channel prices: `price_overrides` table.
- Shipping labels: Shippo or Parcelcraft; `tracking_number` already exists.

## Before go-live (Harry's checklist, not code)
- Washington DOR registration; add WA registration in Stripe Tax; live keys and live webhook
  endpoint subscribed to the three events; test restore done; shipping and returns policy written.
