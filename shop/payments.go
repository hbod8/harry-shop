package shop

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/stripe/stripe-go/v87"
)

// Config comes from the environment (see .env.example).
type Config struct {
	StripeKey     string // STRIPE_SECRET_KEY; card checkout is off without it
	WebhookSecret string // STRIPE_WEBHOOK_SECRET
	SiteURL       string // SITE_URL, for Stripe's success and cancel links
	ShippingCents int64  // SHIPPING_CENTS, flat rate per card order; 0 = none
	AutomaticTax  bool   // STRIPE_AUTOMATIC_TAX=true needs Stripe Tax set up
}

func ConfigFromEnv() Config {
	cfg := Config{
		StripeKey:     os.Getenv("STRIPE_SECRET_KEY"),
		WebhookSecret: os.Getenv("STRIPE_WEBHOOK_SECRET"),
		SiteURL:       strings.TrimRight(os.Getenv("SITE_URL"), "/"),
		ShippingCents: 600,
	}
	if cfg.SiteURL == "" {
		cfg.SiteURL = "http://127.0.0.1:8090"
	}
	if v, err := strconv.ParseInt(os.Getenv("SHIPPING_CENTS"), 10, 64); err == nil && v >= 0 {
		cfg.ShippingCents = v
	}
	cfg.AutomaticTax, _ = strconv.ParseBool(os.Getenv("STRIPE_AUTOMATIC_TAX"))
	return cfg
}

// Payments creates hosted checkout pages. It is an interface so checkout
// can be tested without the network.
type Payments interface {
	CreateSession(ctx context.Context, req SessionRequest) (Session, error)
}

type SessionRequest struct {
	OrderID   string
	Email     string
	Lines     []SessionLine
	ExpiresAt time.Time
}

type SessionLine struct {
	Name      string
	TaxCode   string
	UnitCents int64
	Quantity  int64
}

type Session struct {
	ID  string
	URL string
}

// StripePayments creates Stripe Checkout sessions.
type StripePayments struct {
	client *stripe.Client
	cfg    Config
}

// NewStripePayments returns nil when no secret key is configured.
func NewStripePayments(cfg Config) *StripePayments {
	if cfg.StripeKey == "" {
		return nil
	}
	return &StripePayments{client: stripe.NewClient(cfg.StripeKey), cfg: cfg}
}

func (p *StripePayments) CreateSession(ctx context.Context, req SessionRequest) (Session, error) {
	// with automatic tax, Stripe adds tax on top of our prices
	var taxBehavior *string
	if p.cfg.AutomaticTax {
		taxBehavior = stripe.String("exclusive")
	}

	params := &stripe.CheckoutSessionCreateParams{
		Mode:              stripe.String(string(stripe.CheckoutSessionModePayment)),
		ClientReferenceID: stripe.String(req.OrderID),
		CustomerEmail:     stripe.String(req.Email),
		ExpiresAt:         stripe.Int64(req.ExpiresAt.Unix()),
		SuccessURL:        stripe.String(p.cfg.SiteURL + "/order/?session_id={CHECKOUT_SESSION_ID}"),
		CancelURL:         stripe.String(p.cfg.SiteURL + "/cart/"),
		Metadata:          map[string]string{"order_id": req.OrderID},
		AutomaticTax:      &stripe.CheckoutSessionCreateAutomaticTaxParams{Enabled: stripe.Bool(p.cfg.AutomaticTax)},
		ShippingAddressCollection: &stripe.CheckoutSessionCreateShippingAddressCollectionParams{
			AllowedCountries: stripe.StringSlice([]string{"US"}),
		},
	}
	for _, l := range req.Lines {
		product := &stripe.CheckoutSessionCreateLineItemPriceDataProductDataParams{Name: stripe.String(l.Name)}
		if l.TaxCode != "" {
			product.TaxCode = stripe.String(l.TaxCode)
		}
		params.LineItems = append(params.LineItems, &stripe.CheckoutSessionCreateLineItemParams{
			Quantity: stripe.Int64(l.Quantity),
			PriceData: &stripe.CheckoutSessionCreateLineItemPriceDataParams{
				Currency:    stripe.String(string(stripe.CurrencyUSD)),
				UnitAmount:  stripe.Int64(l.UnitCents),
				ProductData: product,
				TaxBehavior: taxBehavior,
			},
		})
	}
	if p.cfg.ShippingCents > 0 {
		params.ShippingOptions = []*stripe.CheckoutSessionCreateShippingOptionParams{{
			ShippingRateData: &stripe.CheckoutSessionCreateShippingOptionShippingRateDataParams{
				DisplayName: stripe.String("Flat rate shipping"),
				Type:        stripe.String("fixed_amount"),
				TaxBehavior: taxBehavior,
				FixedAmount: &stripe.CheckoutSessionCreateShippingOptionShippingRateDataFixedAmountParams{
					Amount:   stripe.Int64(p.cfg.ShippingCents),
					Currency: stripe.String(string(stripe.CurrencyUSD)),
				},
			},
		}}
	}

	s, err := p.client.V1CheckoutSessions.Create(ctx, params)
	if err != nil {
		return Session{}, err
	}
	if s.URL == "" {
		return Session{}, errors.New("stripe returned a session without a URL")
	}
	return Session{ID: s.ID, URL: s.URL}, nil
}
