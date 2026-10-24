package sourcecheck

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var (
	selectStatement = regexp.MustCompile(`(?is)^SELECT\s+(?:[^;]*\sFROM\s|[a-z_][a-z0-9_]*\s*\()`)
	withStatement   = regexp.MustCompile(`(?is)^WITH(?:\s+RECURSIVE)?\s+[a-z_][a-z0-9_]*\s+AS\s*\(`)
	dmlStatement    = regexp.MustCompile(`(?is)^(?:INSERT\s+INTO|UPDATE\s+[^;\s]+\s+SET|DELETE\s+FROM)\s`)
	ddlStatement    = regexp.MustCompile(`(?is)^(?:CREATE|ALTER|DROP|TRUNCATE)\s+(?:TABLE|INDEX|SCHEMA|TYPE|FUNCTION|TRIGGER|POLICY)\s`)
	transactionSQL  = regexp.MustCompile(`(?is)^(?:SAVEPOINT\s|ROLLBACK\s+TO\s+SAVEPOINT\s|RELEASE\s+SAVEPOINT\s)`)
	queryFragment   = regexp.MustCompile(`(?s)^(?:WHERE|JOIN|LEFT\s+JOIN|RIGHT\s+JOIN|INNER\s+JOIN|CROSS\s+JOIN|ORDER\s+BY|GROUP\s+BY|HAVING|LIMIT|OFFSET)\s`)
)

var rawSQLExecutors = map[string]bool{
	"internal/store/migrate.go":   true,
	"internal/store/partition.go": true,
}

// TestProductionSQLSources scans SQL and Go files in the checkout, including
// untracked and build-tagged files. make source-check disables the Go test cache
// so source changes outside this test package always receive a fresh scan.
func TestProductionSQLSources(t *testing.T) {
	diagnostics, err := checkSQLSources(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range diagnostics {
		t.Error(diagnostic)
	}
}

// Imports, database APIs and file lengths are checked by .golangci.yml.
func checkSQLSources(root string) ([]string, error) {
	set := token.NewFileSet()
	var diagnostics []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relative = filepath.ToSlash(relative)
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == "node_modules" ||
				relative == "internal/db/gen" || relative == "internal/api/gen" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(relative, ".sql") {
			if !strings.HasPrefix(relative, "internal/db/queries/") && !strings.HasPrefix(relative, "internal/store/migrations/") {
				diagnostics = append(diagnostics, relative+": SQL source must live under internal/db/queries or internal/store/migrations")
			}
			return nil
		}
		if !strings.HasSuffix(relative, ".go") || strings.HasSuffix(relative, "_test.go") || rawSQLExecutors[relative] {
			return nil
		}
		file, err := parser.ParseFile(set, path, nil, 0)
		if err != nil {
			return err
		}
		ast.Inspect(file, func(node ast.Node) bool {
			literal, ok := node.(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			value, err := strconv.Unquote(literal.Value)
			if err != nil || !looksLikeSQL(value) {
				return true
			}
			diagnostics = append(diagnostics, fmt.Sprintf(
				"%s:%d: production SQL must originate in internal/db/queries; use a named sqlc query",
				relative, set.Position(literal.Pos()).Line,
			))
			return true
		})
		return nil
	})
	return diagnostics, err
}

func looksLikeSQL(value string) bool {
	trimmed := strings.TrimSpace(value)
	return selectStatement.MatchString(trimmed) || withStatement.MatchString(trimmed) ||
		dmlStatement.MatchString(trimmed) || ddlStatement.MatchString(trimmed) ||
		transactionSQL.MatchString(trimmed) || queryFragment.MatchString(trimmed)
}
