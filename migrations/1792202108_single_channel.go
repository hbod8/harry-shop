package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Each product is paid through exactly one channel: products.channels (a
// list) becomes products.channel. Existing products keep the first channel
// in their list. Checkout derives an order's channel from its products.
func init() {
	m.Register(func(app core.App) error {
		products, err := app.FindCollectionByNameOrId("products")
		if err != nil {
			return err
		}
		channels, err := app.FindCollectionByNameOrId("channels")
		if err != nil {
			return err
		}
		products.Fields.Add(relation("channel", channels, false, false))
		if err := app.Save(products); err != nil {
			return err
		}
		if _, err := app.DB().NewQuery(
			"UPDATE products SET channel = COALESCE(json_extract(channels, '$[0]'), '')").Execute(); err != nil {
			return err
		}
		products.Fields.GetByName("channel").(*core.RelationField).Required = true
		products.Fields.RemoveByName("channels")
		return app.Save(products)
	}, func(app core.App) error {
		products, err := app.FindCollectionByNameOrId("products")
		if err != nil {
			return err
		}
		channels, err := app.FindCollectionByNameOrId("channels")
		if err != nil {
			return err
		}
		products.Fields.Add(multiRelation("channels", channels, false))
		if err := app.Save(products); err != nil {
			return err
		}
		if _, err := app.DB().NewQuery(
			"UPDATE products SET channels = json_array(channel) WHERE channel != ''").Execute(); err != nil {
			return err
		}
		products.Fields.RemoveByName("channel")
		return app.Save(products)
	})
}
