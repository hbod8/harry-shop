# loads .env (Stripe keys etc.) when present
dev:
	set -a; [ -f .env ] && . ./.env; set +a; go run . serve

# forwards Stripe sandbox events to the local webhook (needs the Stripe CLI);
# the live webhook endpoint must subscribe to the same events
STRIPE_EVENTS = checkout.session.completed,checkout.session.async_payment_succeeded,checkout.session.expired,charge.refunded
stripe-listen:
	stripe listen --forward-to localhost:8090/shop/stripe/webhook \
	  --events $(STRIPE_EVENTS)

seed:
	go run . seed

# PocketBase must be running: the catalog is read from its API.
# --cleanDestinationDir removes pages for products that became unlisted.
site:
	hugo --source site --cleanDestinationDir

test:
	go test ./...

build:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o harry-shop .

.PHONY: dev stripe-listen seed site test build
