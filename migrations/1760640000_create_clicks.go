package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	m "github.com/pocketbase/pocketbase/migrations"
	"github.com/pocketbase/pocketbase/tools/types"
)

// Proof of concept counter served entirely by PocketBase's record API.
// Each click is a new row and the count is the list's totalItems, so
// concurrent clicks never overwrite each other and no one can set an
// arbitrary value. Anyone may list and create; no one but a superuser
// may update or delete.
func init() {
	m.Register(func(app core.App) error {
		c := core.NewBaseCollection("clicks")
		c.Fields.Add(&core.AutodateField{Name: "created", OnCreate: true})
		c.ListRule = types.Pointer("")
		c.CreateRule = types.Pointer("")
		return app.Save(c)
	}, func(app core.App) error {
		c, err := app.FindCollectionByNameOrId("clicks")
		if err != nil {
			return err
		}
		return app.Delete(c)
	})
}
