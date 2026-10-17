# Background

Decisions made with Harry in Oct 2026, with reasons, so you don't re-open them.
The longer backend write-up is Harry's doc "PocketBase and Stripe Plan" (claude.ai);
everything needed to build is copied here.

## Who and what
- Harry is a developer who prefers Go, knows Hugo well, and has not done front-end work in about 4 years.
- Lives and sells from Washington state.
- Kinds of selling, eventually:
  1. **Shop**: his own prints and t-shirts, shipped, paid by card through Stripe.
  2. **Group buys**: film chemistry sold at cost, mostly coordinated on Discord. Buyers order with
     just an email at an estimated price and pay by Zelle/Venmo. Harry records payments by hand,
     reprices to the real cost, and buyers see a balance. Local pickup only.
  3. **More storefronts** sharing the same API, e.g. one for the Seattle Photo Club. Each product
     belongs to exactly one storefront.
  4. Later, maybe: an in-person booth (Square or similar) selling from the same stock.
  5. Later, maybe: merch for another business. That business must use **its own** Stripe account
     (seller of record, its own tax). Do not build multi-tenant payments into this app.

### Group-buy cycle (no rounds table)
Items go live → buyers place `unconfirmed` orders at the estimated price → Harry closes the items
(deactivates them) → places the bulk order → reprices the unconfirmed orders → marks them
`confirmed` → `fulfilled` at pickup. No new round until the last is confirmed, by habit, not a
code check. Products are named for their round ("Developer, round 3, Dec 2026") if needed.

## Rejected options (don't suggest them again)
- Shopify, Big Cartel: monthly fees and Harry doesn't own the data.
- Saleor: its Stripe app lacks hosted Checkout, needs Next.js + DynamoDB, 4-6 GB RAM.
- Hand-rolled Go + Postgres: too much to operate for this size.
- React/Angular/Next/SvelteKit/Astro front ends: bring back a Node toolchain.

## Architecture

API first (decided with Harry 2026-10-16): pages are static Hugo HTML; anything live is small
hand-written JS calling PocketBase's built-in REST API. Custom Go routes exist only where we must
talk to Stripe or do money math on the server. htmx and Go-template pages were dropped.

```
browser ── static HTML + JS (Hugo, in pb_public/) ─┐
        ── fetch /api/collections/... (rules) ─────┤  one Go binary (PocketBase v0.40.5)
        ── POST /shop/checkout ── {url} ─> Stripe hosted Checkout
Stripe  ── POST /shop/stripe/webhook ──────────────┘  pb_data/data.db (SQLite)
                                                       └─ Litestream + PB backups -> Backblaze B2
```

- **Public pages (Hugo).** Home, product list, product pages, about, policies, cart, login,
  account, unlisted `/item/`, taxonomy term pages. Hugo pulls the catalog from PocketBase's REST API at build time using a
  content adapter (`content/products/_content.gotmpl` with `resources.GetRemote` + `.AddPage`).
  Rebuild after catalog edits. Output goes to `pb_public/`, served by `apis.Static` on `GET /{path...}`.
- **Live bits (JS + collection API).** Stock badge, cart, account pages, unlisted items. One
  small `site/static/js/api.js` wraps `fetch` (JSON, auth header, error display). No SDK, no build.
- **Custom routes (`shop` package, JSON).** `POST /shop/checkout` (card orders go to
  Stripe, manual orders are created `unconfirmed` for Zelle/Venmo), `POST /shop/stripe/webhook`,
  and a 15-minute cron. Everything else goes through the collection API.
- **Admin is done by hand in PocketBase (`/_/`), not code.** Harry edits the catalog, sets order
  `status`, records manual payments (Zelle, Venmo, cash) as order lines, reprices group-buy lines, adjusts
  stock for manual sales, and writes notes. Code only automates what happens without him: checkout,
  Stripe events and expiring reservations. Stats are view collections Harry adds by migration.

### Auth
- Every customer is a `users` row keyed by email, with or without a password. Ordering with just
  an email finds or creates the user (random password). They sign in later with PocketBase's
  email OTP (one-time code), or a password if they set one.
- PocketBase's own auth API: `auth-with-password`, `request-otp` / `auth-with-otp`, `users`
  create for sign-up, password reset by email. The token is kept in `localStorage` by `api.js` and sent as
  the `Authorization` header, so API rules and `e.Auth` work with no cookie bridge.
- No auth cookies means no CSRF surface on our routes. The cost is that an XSS bug could read the
  token, so never insert API data into the page with `innerHTML`; use `textContent` or build elements.
- Sign-up records `marketing_consent`; a small record hook sets `consent_at` server-side (the
  only planned hook).

### Cart
`localStorage` key `cart` holding `[{variant, qty}]`, max ~20 lines. It needs no protection
because `/shop/checkout` re-validates IDs and reprices everything; a tampered cart can only
produce a different, correctly priced order.

### Unlisted products
`list` rules require `listed = true`; `view` rules don't. An unlisted product is reachable only by
its random 15-character record id (`/item/#<id>`), never by a list query. Variants follow their
product the same way, so a variant list can't leak hidden product ids. Hugo only builds pages for
listed products; unlisted ones render through one generic `/item/` page. This is privacy by
unguessable link, not access control.

### Order pages without an account
Shop buyers don't sign in. Stripe's `success_url` carries `{CHECKOUT_SESSION_ID}`; the `orders`
and `order_lines` view rules also allow `stripe_session_id = @request.query.session_id`, so the
`/order/` page can read that one order through the API. The session id is unguessable.

## Data model (Go migrations)
Money is an integer-only Number field, in cents. Every collection has `id`, `created`, `updated`.
Empty API rule = public; null (locked) = superusers and Go code only. Superusers bypass rules.

```
storefronts ─< products >── product_type (select)
                  │  >─< terms (Hugo taxonomies: tags, themes, collections)
                  │  >─< channels (ways to pay)
                  └─< variants (stock + price, shared by every channel)
users ─< orders >── storefront, channel        users ─< user_notes
          └─< order_lines (the ledger)         stripe_events (standalone)
               order_totals (view: total, paid, balance, payment_status)
```

| Collection | Fields | List / view rule | Writes |
|---|---|---|---|
| `storefronts` | `slug` (unique), `name`, `domain` | public | admin |
| `channels` | `slug` (unique), `name`, `provider` (stripe, manual), `active` | public | admin |
| `terms` | `taxonomy` (text, slug pattern, e.g. tags, themes, collections), `slug`, `name`, `description`; unique (taxonomy, slug) | public | admin |
| `products` | `storefront` (single), `product_type` (select: print, apparel, chemistry), `title`, `slug`, `description` (editor), `images` (files, thumbs 600x0/1200x0), `tax_code`, `terms` (multi), `channels` (multi, required), `listed`, `active`; unique (storefront, slug) | list `active && listed`, view `active` | admin |
| `variants` | `product` (no cascade), `sku` (unique), `name`, `price_cents`, `on_hand`, `reserved`, `position` | list `product.active && product.listed`, view `product.active` | admin and Go |
| `users` (built in) | + `phone`, `discord`, `marketing_consent`, `consent_at`; OTP enabled | own record | sign-up via API; hook sets `consent_at` |
| `user_notes` | `user` (cascade), `order` (optional), `source` (discord, in_person, email, other), `body` | locked | admin |
| `orders` | `storefront`, `channel`, `user` (required), `email`, `name`, `phone`, `ship_line1`, `ship_line2`, `ship_city`, `ship_state`, `ship_postal`, `ship_country`, `status`, `tracking_number`, `stripe_session_id` (unique if set), `stripe_payment_intent`, `stripe_customer`, `expires_at` | own (`user = @request.auth.id`) or matching `?session_id=` | Go and admin |
| `order_lines` | `order` (no cascade), `kind` (item, shipping, tax, payment, refund), `variant` (items), `sku`, `name`, `quantity` (≥ 1), `quoted_cents` (items), `unit_cents`, `channel` (payments and refunds: how the money came in), `external_ref` (unique if set), `note` | via parent order, same as above | Go and admin |
| `order_totals` (view) | per order: `user`, `storefront`, `channel`, `status`, `stripe_session_id`, `total`, `paid`, `balance`, `payment_status` | same as orders | read only |
| `customers` (view) | per user: contact fields, `note_count`, `notes` (all notes, newest first, one per line) | locked | read only |
| `orders_unconfirmed`, `orders_confirmed` (views) | open orders by status, oldest first: customer, `discord`, `items` summary, `total`, `paid`, `balance`, `payment_status` | locked | read only |
| `stats` (view) | per period (last 7 days, last 30 days, this month, quarter, year, local time): `orders` (not cancelled), `revenue` (item lines of confirmed/fulfilled orders minus refunds; no tax, shipping or unconfirmed orders), `new_customers` (first order in period), `returning_customers` (ordered in period and before it) | locked | read only |
| `stripe_events` | `event_id` (unique), `type` | locked | Go only |

- **Taxonomies follow Hugo's model**: a taxonomy (`tags`) has terms (`seattle-rain`) assigned to
  products. `terms.taxonomy` is free text (lowercase slug pattern), so a new taxonomy is just a
  new name typed in `/_/`. Before each Hugo build, a Go command writes the distinct taxonomy names
  into Hugo's `[taxonomies]` config, so term pages exist for every taxonomy in use.
  `product_type` is also published to Hugo as the `types` taxonomy; it is not called `type`
  because that front matter key picks layouts in Hugo.
- **Channels are chosen per order.** A product lists the channels it may be paid through. At
  checkout the cart's channel must be allowed by every product in it; if both card and manual
  fit, the buyer picks; if none fits, checkout refuses ("order these separately"). Stock is
  shared across channels. Per-channel prices are deferred: add a `price_overrides` table
  (variant, channel, cents) if ever needed.
- **Order lines are the ledger.** Every amount on an order is a line: `item`, `shipping` and
  `tax` are charges, `payment` is a credit, `refund` reverses a payment. Amounts are always
  positive (`quantity × unit_cents`); `kind` sets the direction, so Harry never types a minus sign.
  No discount kind yet (add one by migration if needed). Lines don't cascade-delete, so deleting
  an order can't silently delete its payments; delete the lines first.
- **Prices are copied per order.** Item lines carry `quoted_cents` (fixed at order time) and
  `unit_cents` (what's charged). Harry reprices group-buy orders by editing `unit_cents` on
  `unconfirmed` lines in `/_/`.
- **Totals are never stored.** The `order_totals` view computes `total` = items + shipping + tax,
  `paid` = payments − refunds, `balance` = total − paid, and `payment_status`: `unpaid` (no
  payments), `refunded` (paid back to 0 or less), `partial`, `paid`, `overpaid`.
- **Order `status`** (set by Harry in `/_/`; code only sets it for Stripe orders):
  `unconfirmed` (checkout open, or group-buy order placed), `confirmed` (Stripe paid, or Harry
  placed the bulk order), `fulfilled` (shipped or picked up), `cancelled`. Fulfilling with a
  balance owed is allowed; a view of fulfilled orders with `balance > 0` is the warning.
- **Stock** moves automatically only for Stripe orders. Harry adjusts `on_hand` by hand for
  stocked items sold manually; group-buy items don't need it.
- Plain address columns, SKUs, consent and Stripe IDs are deliberate: they map onto
  Shopify/WooCommerce CSV imports if Harry ever moves.

## Shipping
Flat-rate Stripe Checkout `shipping_options` (fixed amounts per order; address-based rates only
work in embedded Checkout). Labels are bought by hand from a CSV export of ready-to-ship orders;
`tracking_number` is pasted back. Stripe has no native label feature; Shippo or Parcelcraft
(EasyPost) can plug in later without schema changes.

## Stripe
- Checkout Session with inline `PriceData` (currency usd, unit amount, `ProductData{Name, TaxCode}`),
  `AutomaticTax` on, shipping address collection US only, `ClientReferenceID = order.Id`,
  `ExpiresAt = now + 30 min` (Stripe's minimum; max 24 h).
- Reserve stock and create the `unconfirmed` order in one `app.RunInTransaction`, with
  `UPDATE variants SET reserved = reserved + q WHERE id = ? AND on_hand - reserved >= q`
  and fail the whole cart if any row isn't affected.
- Webhook: read raw body, `webhook.ConstructEvent(body, sigHeader, whsec)`, then in one
  transaction insert the event id into `stripe_events` (unique-index error = already handled,
  return 200) and handle:
  - `checkout.session.completed`: require `PaymentStatus == "paid"`; copy tax, shipping, total,
    email, shipping details; `on_hand -= q, reserved -= q`; add `tax` and `shipping` lines and a
    `payment` line (`channel = card`, `external_ref` = payment intent id); `status = confirmed`.
  - `checkout.session.expired`: `reserved -= q`; `status = cancelled`.
  - `charge.refunded`: add a `refund` line (`external_ref` = refund id).
- Cron every 15 min releases reservations on unconfirmed Stripe orders past `expires_at` (backup for a
  missed expired event).
- Stripe retries live webhooks up to 3 days and can send duplicates; the unique index absorbs them.
- Local: `stripe listen --forward-to localhost:8090/shop/stripe/webhook`, use the `whsec_...` it prints.
  Test card 4242 4242 4242 4242.

## Tax and legal (not code, but affects launch)
- Washington requires registering with DOR and collecting sales tax from the first sale. Stripe
  Tax calculates it (needs a WA registration added in Stripe). B&O tax is separate.
- Group-buy at-cost sales are probably still retail sales in WA; Harry is asking DOR. Manual
  orders currently have no tax line; Harry can add a `tax` line by hand if DOR says so.

## Hosting
- One VPS with 1-2 GB RAM (for example Hetzner Hillsboro), about $5-8/month. Verify prices.
- `./shop serve shop.example.com` gets Let's Encrypt certs itself; no nginx/Caddy.
- systemd unit, `Restart=always`, env file for secrets. Deploy = copy binary + `pb_public/`, restart.
- Backups: PocketBase scheduled backups to B2 plus Litestream streaming the SQLite file. Test a restore.
- Total about $7-10/month including domain and storage.

## Versions checked on 2026-10-17
PocketBase v0.40.5 (go 1.27 in its go.mod), stripe-go v87.0.0, htmx v4.0.0 (released 2026-08-28;
changed some defaults from 2.x, so read its migration notes rather than 2.x tutorials),
Hugo v0.168.0, Pico CSS v2.1.1. (htmx was dropped on 2026-10-16 for the API-first design.)
