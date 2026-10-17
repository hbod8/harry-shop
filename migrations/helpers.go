package migrations

import (
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/types"
)

const slugPattern = `^[a-z0-9]+(?:-[a-z0-9]+)*$`

// newCollection returns a base collection with the given rules and the
// usual created/updated timestamps. A nil rule locks that action to
// superusers and Go code.
func newCollection(name string, list, view *string) *core.Collection {
	c := core.NewBaseCollection(name)
	c.ListRule = list
	c.ViewRule = view
	return c
}

func addTimestamps(c *core.Collection) {
	c.Fields.Add(
		&core.AutodateField{Name: "created", OnCreate: true},
		&core.AutodateField{Name: "updated", OnCreate: true, OnUpdate: true},
	)
}

func rule(r string) *string { return types.Pointer(r) }

func slugField() *core.TextField {
	return &core.TextField{Name: "slug", Required: true, Max: 100, Pattern: slugPattern}
}

// cents is an integer money field. Non-negative unless signed.
func cents(name string, signed bool) *core.NumberField {
	f := &core.NumberField{Name: name, OnlyInt: true}
	if !signed {
		f.Min = types.Pointer(0.0)
	}
	return f
}

func count(name string) *core.NumberField {
	return &core.NumberField{Name: name, OnlyInt: true, Min: types.Pointer(0.0)}
}

func relation(name string, to *core.Collection, required, cascade bool) *core.RelationField {
	return &core.RelationField{
		Name:          name,
		CollectionId:  to.Id,
		MaxSelect:     1,
		Required:      required,
		CascadeDelete: cascade,
	}
}

func multiRelation(name string, to *core.Collection, required bool) *core.RelationField {
	return &core.RelationField{
		Name:         name,
		CollectionId: to.Id,
		MaxSelect:    999,
		Required:     required,
	}
}

func choice(name string, required bool, values ...string) *core.SelectField {
	return &core.SelectField{Name: name, Values: values, MaxSelect: 1, Required: required}
}

// saveAll saves collections in order, stopping at the first error.
func saveAll(app core.App, cs ...*core.Collection) error {
	for _, c := range cs {
		if err := app.Save(c); err != nil {
			return err
		}
	}
	return nil
}

// deleteAll deletes collections by name in order, ignoring missing ones.
func deleteAll(app core.App, names ...string) error {
	for _, n := range names {
		c, err := app.FindCollectionByNameOrId(n)
		if err != nil {
			continue
		}
		if err := app.Delete(c); err != nil {
			return err
		}
	}
	return nil
}
