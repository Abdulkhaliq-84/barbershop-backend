package database

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Each module's SQL touches its own schema only (docs/architecture/
// persistence.md §2). sqlc can't tell: every module's generated code is
// built from all the migrations, so a query on another module's table
// compiles fine. This test reads the queries instead.
func TestModulesQueryOnlyTheirOwnSchema(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..") // the repository, from internal/platform/database
	var schemas []string
	migrations, err := filepath.Glob(filepath.Join(root, "migrations", "*.sql"))
	if err != nil || len(migrations) == 0 {
		t.Fatalf("migrations: %v", err)
	}
	created := regexp.MustCompile(`(?i)CREATE SCHEMA (?:IF NOT EXISTS )?(\w+)`)
	for _, m := range migrations {
		sql, err := os.ReadFile(m)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range created.FindAllStringSubmatch(string(sql), -1) {
			schemas = append(schemas, match[1])
		}
	}
	// schema.table, not inside a word (a column like "b.business_id" is fine).
	used := regexp.MustCompile(`\b(` + strings.Join(schemas, "|") + `)\.\w+`)

	files, err := filepath.Glob(filepath.Join(root, "internal", "*", "adapters", "postgres", "queries.sql"))
	if err != nil || len(files) < 6 {
		t.Fatalf("queries.sql files: %d, %v", len(files), err)
	}
	for _, f := range files {
		module := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(f))))
		sql, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for i, line := range strings.Split(string(sql), "\n") {
			code, _, _ := strings.Cut(line, "--") // comments may name other modules
			for _, m := range used.FindAllStringSubmatch(code, -1) {
				if m[1] != module {
					t.Errorf("%s:%d: the %s module queries %s", f, i+1, module, m[0])
				}
			}
		}
	}
}
