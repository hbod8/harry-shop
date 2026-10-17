# Background

Decisions made with Harry in Oct 2026, with reasons, so you don't re-open them.
The longer backend write-up is Harry's doc "PocketBase and Stripe Plan" (claude.ai);
everything needed to build is copied here.

## Who and what
- Harry is a developer who prefers Go, knows Hugo well, and has not done front-end work in about 4 years.
- Lives and sells from Washington state.
- Three kinds of selling, eventually:
  1. **Shop**: his own prints and t-shirts, shipped, paid by card through Stripe.
  2. **Community**: group buys of film chemistry sold at cost to a local group. Members sign in, order at an estimated price, pay by Zelle/Venmo before Harry buys. He records payments by hand, then reprices to the real cost; members see a balance. Local pickup only.
  3. Later, maybe: merch for another business. That business must use **its own** Stripe account (seller of record, its own tax). Do not build multi-tenant payments into this app.

## Rejected options (don't suggest them again)
- Shopify, Big Cartel: monthly fees and Harry doesn't own the data.
- Saleor: its Stripe app lacks hosted Checkout, needs Next.js + DynamoDB, 4-6 GB RAM.
- Hand-rolled Go + Postgres: too much to operate for this size.
- React/Angular/Next/SvelteKit/Astro front ends: bring back a Node toolchain.

## Architecture

```
browser ── static HTML (Hugo, in pb_public/) ──┐
        ── htmx fragments / Go pages ──────────┤  one Go binary (PocketBase v0.40.5)
        ── POST /shop/checkout ── 303 ─> Stripe hosted Checkout
Stripe  ── POST /shop/stripe/webhook ──────────┘  pb_data/data.db (SQLite)
                                                   └─ Litestream + PB backups -> Backblaze B2
```

- **Public pages (Hugo).** Home, product list, product pages, about, policies. Hugo pulls the
  catalog from PocketBase's REST API at build time using a content adapter
  (`content/products/_content.gotmpl` with `resources.GetRemote` + `.AddPage`). Rebuild after
  catalog edits. Output goes to `pb_public/`, served by `apis.Static` on `GET /{path...}`.
- **Live bits (htmx).** Stock badge, add-to-cart, cart page body: Hugo pages contain
  `hx-get`/`hx-post` attributes that hit `/shop/...` routes returning HTML fragments from Go templates.
- **Signed-in pages (Go templates).** Login, "My orders", order detail with balance, Community
  catalog. Rendered fully by Go, sharing the same CSS and base layout look.
- **Admin.** PocketBase's built-in admin UI (`/_/`) for catalog, payments and orders, plus one
  custom reprice route.

### Auth for server-rendered pages
PocketBase v0.40.5 reads auth only from the `Authorization` header (`apis/middlewares.go`,
`loadAuthToken`). For HTML pages we need a cookie:
- `POST /shop/login` form: `app.FindAuthRecordByEmail("users", email)`, `rec.ValidatePassword(pw)`,
  `rec.NewAuthToken()`, set cookie `pb_auth` (HttpOnly, Secure in prod, SameSite=Lax, Path=/).
- A router middleware with priority `apis.DefaultLoadAuthTokenMiddlewarePriority - 1` that copies
  the cookie into the `Authorization` header when the header is empty. Then `e.Auth` and API rules
  work normally.
- `POST /shop/logout` clears the cookie.
- CSRF: SameSite=Lax plus rejecting POSTs to `/shop/*` (except the webhook) whose `Origin` header
  doesn't match the site.

### Cart
Unsigned cookie `cart` holding `[{variant_id, qty}]`, max ~20 lines. It doesn't need signing
because the server re-validates IDs and reprices everything at checkout; a tampered cookie can
only produce a different, correctly priced cart.

## Data model (Go migrations)
Money is a Number field with "no decimals", in cents. Empty API rule = public; null (locked) =
superusers and Go code only.

| Collection | Key fields | Read rule | Write |
|---|---|---|---|
| `channels` | `slug`, `name`, `payment_mode` (stripe, manual), `requires_account` | public | admin UI |
| `products` | `title`, `slug`, `description`, `images` (file, multiple), `tax_code`, `active` | `active = true` | admin UI |
| `variants` | `product` (relation), `sku` (unique), `name`, `on_hand`, `reserved` | public | admin UI and Go |
| `prices` | `channel`, `variant`, `amount_cents`, `active`; unique (channel, variant) | `active = true && (channel.requires_account = false \|\| @request.auth.id != "")` | admin UI |
| `users` (built in) | `name`, `phone`, `marketing_consent`, `consent_at` | own record | sign-up |
| `orders` | `channel`, `user` (optional), `email`, `name`, `phone`, `ship_line1`, `ship_line2`, `ship_city`, `ship_state`, `ship_postal`, `ship_country`, `status`, `subtotal_cents`, `tax_cents`, `shipping_cents`, `total_cents`, `stripe_session_id`, `stripe_payment_intent`, `stripe_customer`, `expires_at` | `user = @request.auth.id` | Go only |
| `order_lines` | `order` (cascade delete), `variant`, `sku`, `name`, `quantity`, `unit_cents` | `order.user = @request.auth.id` | Go and admin UI |
| `payments` | `order`, `method` (stripe, zelle, venmo, cash), `amount_cents` (negative = refund), `external_ref` (unique), `note` | `order.user = @request.auth.id` | Go and admin UI |
| `stripe_events` | `event_id` (unique), `type` | locked | Go only |

- Order `status`: `pending` (Stripe session open), `awaiting_payment` (Community), `paid`,
  `fulfilled`, `cancelled`.
- Balance = `total_cents - sum(payments.amount_cents)`, computed in Go, never stored.
- Plain address columns, SKUs, consent and Stripe IDs are deliberate: they map onto
  Shopify/WooCommerce CSV imports if Harry ever moves.
- `slug` on `products` is an addition to the original doc, needed for Hugo URLs.

## Stripe
- Checkout Session with inline `PriceData` (currency usd, unit amount, `ProductData{Name, TaxCode}`),
  `AutomaticTax` on, shipping address collection US only, `ClientReferenceID = order.Id`,
  `ExpiresAt = now + 30 min` (Stripe's minimum; max 24 h).
- Reserve stock and create the `pending` order in one `app.RunInTransaction`, with
  `UPDATE variants SET reserved = reserved + q WHERE id = ? AND on_hand - reserved >= q`
  and fail the whole cart if any row isn't affected.
- Webhook: read raw body, `webhook.ConstructEvent(body, sigHeader, whsec)`, then in one
  transaction insert the event id into `stripe_events` (unique-index error = already handled,
  return 200) and handle:
  - `checkout.session.completed`: require `PaymentStatus == "paid"`; copy tax, shipping, total,
    email, shipping details; `on_hand -= q, reserved -= q`; insert `payments` row
    (`external_ref` = payment intent id); `status = paid`.
  - `checkout.session.expired`: `reserved -= q`; `status = cancelled`.
  - `charge.refunded`: insert a negative `payments` row.
- Cron every 15 min releases reservations on `pending` orders past `expires_at` (backup for a
  missed expired event).
- Stripe retries live webhooks up to 3 days and can send duplicates; the unique index absorbs them.
- Local: `stripe listen --forward-to localhost:8090/shop/stripe/webhook`, use the `whsec_...` it prints.
  Test card 4242 4242 4242 4242.

## Tax and legal (not code, but affects launch)
- Washington requires registering with DOR and collecting sales tax from the first sale. Stripe
  Tax calculates it (needs a WA registration added in Stripe). B&O tax is separate.
- Community at-cost sales are probably still retail sales in WA; Harry is asking DOR. Community
  orders currently have no tax line. Keep a `tax_cents` field usable for them later.

## Hosting
- One VPS with 1-2 GB RAM (for example Hetzner Hillsboro), about $5-8/month. Verify prices.
- `./shop serve shop.example.com` gets Let's Encrypt certs itself; no nginx/Caddy.
- systemd unit, `Restart=always`, env file for secrets. Deploy = copy binary + `pb_public/`, restart.
- Backups: PocketBase scheduled backups to B2 plus Litestream streaming the SQLite file. Test a restore.
- Total about $7-10/month including domain and storage.

## Versions checked on 2026-10-17
PocketBase v0.40.5 (go 1.27 in its go.mod), stripe-go v87.0.0, htmx v4.0.0 (released 2026-08-28;
changed some defaults from 2.x, so read its migration notes rather than 2.x tutorials),
Hugo v0.168.0, Pico CSS v2.1.1.
