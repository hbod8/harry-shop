package main

import (
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/pocketbase/pocketbase"
	"github.com/pocketbase/pocketbase/apis"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/plugins/migratecmd"
	"github.com/pocketbase/pocketbase/tools/hook"
	"github.com/pocketbase/pocketbase/tools/osutils"
	"github.com/spf13/cobra"

	_ "github.com/hbod8/harry-shop/migrations"
	"github.com/hbod8/harry-shop/shop"
)

func main() {
	app := pocketbase.New()

	migratecmd.MustRegister(app, app.RootCmd, migratecmd.Config{
		TemplateLang: migratecmd.TemplateLangGo,
		Automigrate:  osutils.IsProbablyGoRun(),
	})

	shop.RegisterHooks(app)

	cfg := shop.ConfigFromEnv()
	var payments shop.Payments
	if p := shop.NewStripePayments(cfg); p != nil {
		payments = p
	}

	app.OnServe().BindFunc(func(se *core.ServeEvent) error {
		if payments == nil {
			se.App.Logger().Warn("STRIPE_SECRET_KEY not set: card checkout is disabled")
		}
		shop.Register(se, cfg, payments)
		return se.Next()
	})

	// backstop for missed checkout.session.expired events
	app.Cron().MustAdd("release_expired", "*/15 * * * *", func() {
		if n, err := shop.ReleaseExpired(app, time.Now()); err != nil {
			app.Logger().Error("releasing expired orders", "error", err)
		} else if n > 0 {
			app.Logger().Info("released expired orders", "count", n)
		}
	})

	app.RootCmd.AddCommand(&cobra.Command{
		Use:   "seed",
		Short: "Add sample products, customers and orders (local development only)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := app.RunAllMigrations(); err != nil {
				return err
			}
			if err := shop.Seed(app); err != nil {
				return err
			}
			log.Println("sample data added")
			return nil
		},
	})

	// static Hugo output; registered last so PocketBase's own routes win
	app.OnServe().Bind(&hook.Handler[*core.ServeEvent]{
		Func: func(e *core.ServeEvent) error {
			if !e.Router.HasRoute(http.MethodGet, "/{path...}") {
				e.Router.GET("/{path...}", apis.Static(os.DirFS(publicDir()), false))
			}
			return e.Next()
		},
		Priority: 999,
	})

	if err := app.Start(); err != nil {
		log.Fatal(err)
	}
}

// pb_public sits next to the binary, or in the cwd under `go run`.
func publicDir() string {
	if osutils.IsProbablyGoRun() {
		return "./pb_public"
	}
	return filepath.Join(os.Args[0], "../pb_public")
}
