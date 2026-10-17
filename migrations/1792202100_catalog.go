package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
)

// Catalog: storefronts, channels, taxonomy terms, products and variants.
// Lists hide unlisted products (and their variants); view by id does not,
// so an unlisted product is reachable only by its random record id.
func init() {
	m.Register(func(app core.App) error {
		storefronts := newCollection("storefronts", rule(""), rule(""))
		storefronts.Fields.Add(
			slugField(),
			&core.TextField{Name: "name", Required: true, Max: 200},
			&core.TextField{Name: "domain", Max: 255},
		)
		addTimestamps(storefronts)
		storefronts.AddIndex("idx_storefronts_slug", true, "slug", "")

		channels := newCollection("channels", rule(""), rule(""))
		channels.Fields.Add(
			slugField(),
			&core.TextField{Name: "name", Required: true, Max: 200},
			choice("provider", true, "stripe", "manual"),
			&core.BoolField{Name: "active"},
		)
		addTimestamps(channels)
		channels.AddIndex("idx_channels_slug", true, "slug", "")

		// Mirrors Hugo: a taxonomy (e.g. "tags") has terms, which are assigned
		// to products. Taxonomy names are free text; the Hugo build declares
		// whichever names exist.
		terms := newCollection("terms", rule(""), rule(""))
		terms.Fields.Add(
			&core.TextField{Name: "taxonomy", Required: true, Max: 50, Pattern: slugPattern},
			slugField(),
			&core.TextField{Name: "name", Required: true, Max: 200},
			&core.TextField{Name: "description", Max: 2000},
		)
		addTimestamps(terms)
		terms.AddIndex("idx_terms_taxonomy_slug", true, "taxonomy, slug", "")

		if err := saveAll(app, storefronts, channels, terms); err != nil {
			return err
		}

		products := newCollection("products",
			rule("active = true && listed = true"),
			rule("active = true"),
		)
		products.Fields.Add(
			relation("storefront", storefronts, true, false),
			// not "type": that front matter key picks layouts in Hugo
			choice("product_type", true, "print", "apparel", "chemistry"),
			&core.TextField{Name: "title", Required: true, Max: 200},
			slugField(),
			&core.EditorField{Name: "description"},
			&core.FileField{
				Name:      "images",
				MaxSelect: 10,
				MaxSize:   20 << 20,
				MimeTypes: []string{"image/jpeg", "image/png", "image/webp"},
				Thumbs:    []string{"600x0", "1200x0"},
			},
			&core.TextField{Name: "tax_code", Max: 50},
			multiRelation("terms", terms, false),
			multiRelation("channels", channels, true),
			&core.BoolField{Name: "listed"},
			&core.BoolField{Name: "active"},
		)
		addTimestamps(products)
		products.AddIndex("idx_products_storefront_slug", true, "storefront, slug", "")

		if err := app.Save(products); err != nil {
			return err
		}

		variants := newCollection("variants",
			rule("product.active = true && product.listed = true"),
			rule("product.active = true"),
		)
		variants.Fields.Add(
			// no cascade: order lines keep pointing at retired variants
			relation("product", products, true, false),
			&core.TextField{Name: "sku", Required: true, Max: 100},
			&core.TextField{Name: "name", Required: true, Max: 200},
			cents("price_cents", false),
			count("on_hand"),
			count("reserved"),
			count("position"),
		)
		addTimestamps(variants)
		variants.AddIndex("idx_variants_sku", true, "sku", "")

		return app.Save(variants)
	}, func(app core.App) error {
		return deleteAll(app, "variants", "products", "terms", "channels", "storefronts")
	})
}
