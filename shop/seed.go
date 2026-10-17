// Package shop holds hooks, custom routes and dev tooling served alongside PocketBase.
package shop

import (
	"errors"
	"fmt"

	"github.com/pocketbase/pocketbase/core"
)

// SeedGuestSession is the fake Stripe session id on the sample guest order.
const SeedGuestSession = "cs_test_seed_guest_session"

// ErrAlreadySeeded is returned when the sample data is already present.
var ErrAlreadySeeded = errors.New("sample data already present")

// Seed adds dev sample data: terms, products, variants, two customers with
// notes, and four orders: an unconfirmed group buy ($36, $20 paid by Zelle),
// an unconfirmed guest card order ($35 + $6 shipping), a confirmed paid tee
// and a fulfilled print.
// It is for local development only and is never run as a migration.
// Every record gets a generated id; find them by slug, sku or email.
func Seed(app core.App) error {
	if _, err := app.FindFirstRecordByData("products", "slug", "pike-place-at-dawn"); err == nil {
		return ErrAlreadySeeded
	}

	return app.RunInTransaction(func(tx core.App) error {
		s := seeder{app: tx}

		harry := s.find("storefronts", "harry")
		card := s.find("channels", "card")
		manual := s.find("channels", "manual")

		rain := s.add("terms", map[string]any{"taxonomy": "tags", "slug": "seattle-rain", "name": "Seattle Rain"})
		bw := s.add("terms", map[string]any{"taxonomy": "tags", "slug": "black-and-white", "name": "Black and White"})
		noir := s.add("terms", map[string]any{"taxonomy": "themes", "slug": "film-noir", "name": "Film Noir"})
		holiday := s.add("terms", map[string]any{"taxonomy": "collections", "slug": "holiday-2026", "name": "Holiday 2026", "description": "Gift picks for the 2026 holidays."})

		product := func(productType, title, slug string, terms, channels []string, listed, active bool) string {
			return s.add("products", map[string]any{
				"storefront":   harry,
				"product_type": productType,
				"title":        title,
				"slug":         slug,
				"description":  "<p>" + title + ".</p>",
				"terms":        terms,
				"channels":     channels,
				"listed":       listed,
				"active":       active,
			})
		}
		variant := func(productID, sku, name string, price, onHand, position int) string {
			return s.add("variants", map[string]any{
				"product": productID, "sku": sku, "name": name,
				"price_cents": price, "on_hand": onHand, "position": position,
			})
		}

		pike := product("print", "Pike Place at Dawn", "pike-place-at-dawn", []string{rain, holiday}, []string{card}, true, true)
		pike8x10 := variant(pike, "PRINT-PIKE-8X10", "8×10", 3500, 5, 1)
		variant(pike, "PRINT-PIKE-11X14", "11×14", 6000, 2, 2)

		rainier := product("print", "Rainier from Kerry Park", "rainier-from-kerry-park", []string{rain, bw, noir}, []string{card}, true, true)
		rainier8x10 := variant(rainier, "PRINT-RAINIER-8X10", "8×10", 3500, 3, 1)

		tee := product("apparel", "Harry Logo Tee", "harry-logo-tee", []string{holiday}, []string{card, manual}, true, true)
		variant(tee, "TEE-LOGO-S", "S", 2800, 4, 1)
		teeM := variant(tee, "TEE-LOGO-M", "M", 2800, 6, 2)
		variant(tee, "TEE-LOGO-L", "L", 2800, 0, 3)

		developer := product("chemistry", "Developer, round 1, Nov 2026", "developer-round-1", nil, []string{manual}, false, true)
		developer1L := variant(developer, "CHEM-DEV-R1-1L", "1 L", 1800, 20, 1)

		retired := product("print", "Retired Print", "retired-print", nil, []string{card}, true, false)
		variant(retired, "PRINT-RETIRED-8X10", "8×10", 3000, 1, 1)

		sam := s.user("sam@example.com", "Sam Buyer", "sam.buyer")
		guest := s.user("guest@example.com", "Guest Buyer", "")

		groupOrder := s.add("orders", map[string]any{
			"storefront": harry, "channel": manual, "user": sam,
			"email": "sam@example.com", "name": "Sam Buyer", "status": "unconfirmed",
		})
		s.add("order_lines", map[string]any{
			"order": groupOrder, "kind": "item", "variant": developer1L,
			"sku": "CHEM-DEV-R1-1L", "name": "Developer, round 1, Nov 2026 — 1 L",
			"quantity": 2, "quoted_cents": 1800, "unit_cents": 1800,
		})
		s.add("order_lines", map[string]any{
			"order": groupOrder, "kind": "payment", "name": "Zelle from Sam",
			"quantity": 1, "unit_cents": 2000, "channel": manual,
			"external_ref": "ZELLE-1001", "note": "half up front",
		})
		s.add("user_notes", map[string]any{
			"user": sam, "order": groupOrder, "source": "discord",
			"body": "Asked to pick up Saturday at the club meeting.",
		})
		s.add("user_notes", map[string]any{
			"user": sam, "source": "in_person", "body": "Met at the October photo walk.",
		})

		// confirmed: bulk order placed, waiting for pickup
		confirmedOrder := s.add("orders", map[string]any{
			"storefront": harry, "channel": manual, "user": sam,
			"email": "sam@example.com", "name": "Sam Buyer", "status": "confirmed",
		})
		s.add("order_lines", map[string]any{
			"order": confirmedOrder, "kind": "item", "variant": teeM,
			"sku": "TEE-LOGO-M", "name": "Harry Logo Tee — M",
			"quantity": 1, "quoted_cents": 2800, "unit_cents": 2800,
		})
		s.add("order_lines", map[string]any{
			"order": confirmedOrder, "kind": "payment", "name": "Cash at meetup",
			"quantity": 1, "unit_cents": 2800, "channel": manual,
		})

		// fulfilled: done, so it appears in neither open-orders view
		fulfilledOrder := s.add("orders", map[string]any{
			"storefront": harry, "channel": card, "user": guest,
			"email": "guest@example.com", "name": "Guest Buyer", "status": "fulfilled",
			"tracking_number": "9400100000000000000000",
		})
		s.add("order_lines", map[string]any{
			"order": fulfilledOrder, "kind": "item", "variant": rainier8x10,
			"sku": "PRINT-RAINIER-8X10", "name": "Rainier from Kerry Park — 8×10",
			"quantity": 1, "quoted_cents": 3500, "unit_cents": 3500,
		})
		s.add("order_lines", map[string]any{
			"order": fulfilledOrder, "kind": "payment", "name": "Stripe",
			"quantity": 1, "unit_cents": 3500, "channel": card, "external_ref": "pi_test_seed_fulfilled",
		})

		guestOrder := s.add("orders", map[string]any{
			"storefront": harry, "channel": card, "user": guest,
			"email": "guest@example.com", "name": "Guest Buyer", "status": "unconfirmed",
			"stripe_session_id": SeedGuestSession,
		})
		s.add("order_lines", map[string]any{
			"order": guestOrder, "kind": "item", "variant": pike8x10,
			"sku": "PRINT-PIKE-8X10", "name": "Pike Place at Dawn — 8×10",
			"quantity": 1, "quoted_cents": 3500, "unit_cents": 3500,
		})
		s.add("order_lines", map[string]any{
			"order": guestOrder, "kind": "shipping", "name": "Flat rate shipping",
			"quantity": 1, "unit_cents": 600,
		})

		return s.err
	})
}

// seeder keeps the first error so the sample data reads as a flat list.
type seeder struct {
	app core.App
	err error
}

func (s *seeder) find(collection, slug string) string {
	if s.err != nil {
		return ""
	}
	rec, err := s.app.FindFirstRecordByData(collection, "slug", slug)
	if err != nil {
		s.err = fmt.Errorf("%s %q: %w", collection, slug, err)
		return ""
	}
	return rec.Id
}

func (s *seeder) add(collection string, data map[string]any) string {
	if s.err != nil {
		return ""
	}
	c, err := s.app.FindCollectionByNameOrId(collection)
	if err != nil {
		s.err = err
		return ""
	}
	rec := core.NewRecord(c)
	rec.Load(data)
	if err := s.app.Save(rec); err != nil {
		s.err = fmt.Errorf("seed %s: %w", collection, err)
		return ""
	}
	return rec.Id
}

func (s *seeder) user(email, name, discord string) string {
	if s.err != nil {
		return ""
	}
	c, err := s.app.FindCollectionByNameOrId("users")
	if err != nil {
		s.err = err
		return ""
	}
	rec := core.NewRecord(c)
	rec.SetEmail(email)
	rec.SetRandomPassword()
	rec.Set("name", name)
	rec.Set("discord", discord)
	if err := s.app.Save(rec); err != nil {
		s.err = fmt.Errorf("seed user %s: %w", email, err)
		return ""
	}
	return rec.Id
}
