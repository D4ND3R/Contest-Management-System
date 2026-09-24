# Migrations

Forward-only, versioned SQL migrations embedded in the binary and applied by
`cms migrate` (or `cmsctl bootstrap`). Files are named `NNNN_description.sql`
and run in order, each inside its own transaction, under a PostgreSQL
advisory lock so concurrent starters are safe. Never edit a migration that
has shipped; add a new one.
