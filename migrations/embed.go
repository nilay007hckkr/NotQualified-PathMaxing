// Package migrations embeds the SQL migrations into the binary.
//
// Each file is named after the commit that adds it (C03_core_schema.sql,
// C09_click_events.sql, ...), so sorting by name applies them in commit
// order. A migration is never edited after its commit; a later one ALTERs
// what an earlier one created. This Go file lives here, next to the SQL,
// because go:embed can't reach files in a parent directory.
package migrations

import "embed"

// FS holds every *.sql file in this directory.
//
//go:embed *.sql
var FS embed.FS
