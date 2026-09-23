// Package migrations holds the SQL schema migrations (goose format) and embeds
// them into the binary, so `server migrate` needs no files on disk.
//
// Rules: one global ordered sequence (NNNNN_<module>_<what>.sql), forward-only
// once merged (add a new migration, never edit an old one), and backward
// compatible with the previous release (expand → migrate → contract).
package migrations

import "embed"

// FS contains every *.sql migration in this directory.
//
//go:embed *.sql
var FS embed.FS
