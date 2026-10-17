package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Real configuration every environment needs (dev sample products come
// from `make seed` instead): the harry storefront and the two channels.
func init() {
	m.Register(func(app core.App) error {
		rows := []struct {
			collection string
			data       map[string]any
		}{
			{"storefronts", map[string]any{"slug": "harry", "name": "Harry's shop"}},
			{"channels", map[string]any{"slug": "card", "name": "Card", "provider": "stripe", "active": true}},
			{"channels", map[string]any{"slug": "manual", "name": "Zelle / Venmo", "provider": "manual", "active": true}},
		}
		for _, r := range rows {
			c, err := app.FindCollectionByNameOrId(r.collection)
			if err != nil {
				return err
			}
			rec := core.NewRecord(c)
			rec.Load(r.data)
			if err := app.Save(rec); err != nil {
				return err
			}
		}
		return nil
	}, func(app core.App) error {
		for _, q := range []struct{ collection, slug string }{
			{"channels", "manual"}, {"channels", "card"}, {"storefronts", "harry"},
		} {
			rec, err := app.FindFirstRecordByData(q.collection, "slug", q.slug)
			if err != nil {
				continue
			}
			if err := app.Delete(rec); err != nil {
				return err
			}
		}
		return nil
	})
}
