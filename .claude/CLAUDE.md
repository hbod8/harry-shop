# harry-shop

Harry's personal shop: his own photo prints and t-shirts, plus a sign-in "Community"
group buy for film chemistry (paid by Zelle/Venmo, local pickup). Single seller,
low volume (under $1,000/month at first).

Read before working:
- `.claude/plan.md`: what to build, in order, with how to test each step.
- `.claude/background.md`: decisions already made and why, schema, Stripe and tax notes.

## Hard requirements (from Harry)
- Total running cost no more than $20/month.
- Harry owns the data: catalog, customers and orders live in our SQLite file, not in a SaaS.
- Keep it simple. One Go binary, one SQLite file, one small VPS.

## Stack (decided, do not swap without asking Harry)
- Go, with PocketBase **v0.40.5** imported as a library (pinned; pre-1.0, read release notes before upgrading). Its go.mod needs Go 1.27.
- Stripe hosted Checkout via `github.com/stripe/stripe-go/v87`. No card data touches our server.
- Hugo for public catalog pages, built into `pb_public/` and served by PocketBase.
- htmx 4.0.0 (vendored file, pinned) for dynamic fragments. Pico CSS v2 (vendored) for styling.
- Go `html/template` (PocketBase `tools/template`) for signed-in and dynamic pages.
- No Node build chain, no React/Vue/Svelte, no npm. Harry dislikes large JS frameworks.

## Rules
- The browser sends only variant IDs and quantities. The server always looks up prices and stock.
- Only the verified Stripe webhook marks an order paid. The success page only reads status.
- Money is integer cents everywhere. Never floats.
- Schema changes are Go migrations in `migrations/` (committed). Never edit the schema only in the admin UI.
- Secrets (Stripe keys, webhook secret) come from env vars / an untracked `.env`. Never commit them.
- Use a Stripe sandbox (test keys) for all local work.
- `go test ./...` must pass before a step is called done.
