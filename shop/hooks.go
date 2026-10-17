package shop

import "github.com/pocketbase/pocketbase/core"

// firstOrderNumber matches the backfill in migrations/1792202107_order_numbers.go.
const firstOrderNumber = 1001

// RegisterHooks binds the record hooks. They run for every save: checkout,
// the seed, and edits in the admin UI.
func RegisterHooks(app core.App) {
	// Sequential order numbers for customer support. Saves are serialized by
	// PocketBase's single writer, and a unique index backs this up.
	app.OnRecordCreate("orders").BindFunc(func(e *core.RecordEvent) error {
		if e.Record.GetInt("number") == 0 {
			var last int
			if err := e.App.DB().NewQuery("SELECT COALESCE(MAX(number), 0) FROM orders").Row(&last); err != nil {
				return err
			}
			e.Record.Set("number", max(last+1, firstOrderNumber))
		}
		return e.Next()
	})
}
