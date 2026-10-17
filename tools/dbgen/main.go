// Command dbgen regenerates the go-jet code from the migrations. It starts a throwaway
// Postgres in Docker, applies the migrations and generates into the postgres adapter.
// Run with `make db-gen`.
package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/go-jet/jet/v2/generator/metadata"
	jetpostgres "github.com/go-jet/jet/v2/generator/postgres"
	"github.com/go-jet/jet/v2/generator/template"
	jet "github.com/go-jet/jet/v2/postgres"
	"github.com/shopspring/decimal"

	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres"
	"github.com/lein3000zzz/polythought-go/internal/adapters/postgres/pgtest"
)

func main() {
	dir := flag.String("dir", "internal/adapters/postgres/gen", "output directory")
	flag.Parse()

	if err := run(context.Background(), *dir); err != nil {
		log.Fatal(err)
	}
}

func run(ctx context.Context, dir string) error {
	pg, err := pgtest.Start(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = pg.Terminate(ctx) }()

	db, err := postgres.Open(ctx, pg.DSN)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close(ctx) }()

	applied, err := db.Migrate(ctx)
	if err != nil {
		return err
	}

	log.Printf("applied migrations %v", applied)

	// Generate into a fresh directory so dropped tables do not leave stale files behind.
	if err := os.RemoveAll(dir); err != nil {
		return err
	}

	if err := jetpostgres.GenerateDB(db.SQL, "public", dir, skipTables("goose_db_version")); err != nil {
		return err
	}

	log.Printf("generated %s", dir)

	return nil
}

// skipTables leaves tables out of the generated code, such as goose's bookkeeping table,
// which nothing should query. Money columns, numeric, are decimals: a float would round.
func skipTables(names ...string) template.Template {
	skip := make(map[string]bool, len(names))
	for _, n := range names {
		skip[n] = true
	}

	return template.Default(jet.Dialect).UseSchema(func(schema metadata.Schema) template.Schema {
		return template.DefaultSchema(schema).
			UseModel(template.DefaultModel().UseTable(func(t metadata.Table) template.TableModel {
				m := template.DefaultTableModel(t)
				m.Skip = skip[t.Name]

				return m.UseField(func(c metadata.Column) template.TableModelField {
					f := template.DefaultTableModelField(c)
					if c.DataType.Name == "numeric" {
						f.Type = template.NewType(decimal.Decimal{})
					}

					return f
				})
			})).
			UseSQLBuilder(template.DefaultSQLBuilder().UseTable(func(t metadata.Table) template.TableSQLBuilder {
				b := template.DefaultTableSQLBuilder(t)
				b.Skip = skip[t.Name]

				return b
			}))
	})
}
