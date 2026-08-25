package cli

import (
	"strings"

	"github.com/spf13/cobra"

	"github.com/p3bot/tk/internal/index"
)

func newQueryCmd(app *App) *cobra.Command {
	var schema bool
	cmd := &cobra.Command{
		Use:   "query <sql>",
		Short: "Run a read-only SQL query over the index (debug / human ad-hoc)",
		Long: "Run a read-only SELECT (or read-only EXPLAIN/PRAGMA) over the machine-wide\n" +
			"index and print tab-separated rows. Writes are rejected — the index is a\n" +
			"derived cache; durable change is the ticket files or tk doctor --repair.\n\n" +
			"The schema is NOT a stable API: it is rebuilt on any schema_version bump and\n" +
			"may reshape between releases with no migration. Do not script against it —\n" +
			"agents use deps / list / search / next / get / meta. `tk query --schema`\n" +
			"prints the current shape. No ambient --scope flag (filter in SQL).",
		Args: anyArgs(),
		RunE: func(c *cobra.Command, args []string) error {
			return runQuery(app, c, args, schema)
		},
	}
	cmd.Flags().BoolVar(&schema, "schema", false, "print the current (non-stable) index schema and exit")
	return cmd
}

func runQuery(app *App, c *cobra.Command, args []string, schema bool) error {
	if schema {
		if _, err := c.OutOrStdout().Write([]byte(index.SchemaText)); err != nil {
			return err
		}
		return nil
	}

	sqlText := strings.TrimSpace(strings.Join(args, " "))
	if sqlText == "" {
		return usageErrorf("query needs a SQL statement (or --schema)")
	}

	e, err := app.openEngine(c)
	if err != nil {
		return err
	}
	defer e.close()

	// Ad-hoc query needs fresh state across every registered scope.
	if _, err := e.reconcile(c, e.allTargets()); err != nil {
		return err
	}

	result, err := e.db.RunReadOnlyQuery(sqlText)
	if err != nil {
		return err
	}
	if len(result.Columns) > 0 {
		stdoutln(c, strings.Join(result.Columns, "\t"))
	}
	for _, row := range result.Rows {
		stdoutln(c, strings.Join(row, "\t"))
	}
	return nil
}
