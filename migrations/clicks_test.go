package migrations_test

import (
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tests"

	_ "github.com/hbod8/harry-shop/migrations"
)

const clicksURL = "/api/collections/clicks/records"

func newTestApp(t testing.TB) *tests.TestApp {
	app, err := tests.NewTestApp()
	if err != nil {
		t.Fatal(err)
	}
	if err := app.RunAppMigrations(); err != nil {
		t.Fatal(err)
	}
	return app
}

const seededID = "seededclick0001"

func addClick(t testing.TB, app core.App, optID ...string) {
	c, err := app.FindCollectionByNameOrId("clicks")
	if err != nil {
		t.Fatal(err)
	}
	r := core.NewRecord(c)
	if len(optID) > 0 {
		r.Id = optID[0]
	}
	if err := app.Save(r); err != nil {
		t.Fatal(err)
	}
}

func TestClicksRules(t *testing.T) {
	seed := func(t testing.TB, app *tests.TestApp, e *core.ServeEvent) {
		addClick(t, app, seededID)
		addClick(t, app)
	}

	scenarios := []tests.ApiScenario{
		{
			Name:            "anyone can read the count",
			Method:          http.MethodGet,
			URL:             clicksURL + "?perPage=1&fields=id",
			BeforeTestFunc:  seed,
			ExpectedStatus:  200,
			ExpectedContent: []string{`"totalItems":2`},
		},
		{
			Name:            "anyone can add a click",
			Method:          http.MethodPost,
			URL:             clicksURL,
			Body:            strings.NewReader(`{}`),
			Headers:         map[string]string{"Content-Type": "application/json"},
			ExpectedStatus:  200,
			ExpectedContent: []string{`"collectionName":"clicks"`},
		},
		{
			Name:            "anonymous update is forbidden",
			Method:          http.MethodPatch,
			URL:             clicksURL + "/" + seededID,
			BeforeTestFunc:  seed,
			Body:            strings.NewReader(`{}`),
			Headers:         map[string]string{"Content-Type": "application/json"},
			ExpectedStatus:  403,
			ExpectedContent: []string{`"data":{}`},
		},
		{
			Name:            "anonymous delete is forbidden",
			Method:          http.MethodDelete,
			URL:             clicksURL + "/" + seededID,
			BeforeTestFunc:  seed,
			ExpectedStatus:  403,
			ExpectedContent: []string{`"data":{}`},
		},
	}

	for _, s := range scenarios {
		s.TestAppFactory = newTestApp
		s.Test(t)
	}
}

func TestClicksConcurrent(t *testing.T) {
	app := newTestApp(t)
	defer app.Cleanup()

	const n = 50
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { addClick(t, app) })
	}
	wg.Wait()

	total, err := app.CountRecords("clicks")
	if err != nil {
		t.Fatal(err)
	}
	if total != n {
		t.Fatalf("clicks = %d, want %d", total, n)
	}
}
