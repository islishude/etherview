// Package sourcecheck enforces repository-owned production source boundaries.
package sourcecheck

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

const (
	maximumProductionGoLines = 1500
	maximumTestGoLines       = 2500
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

var sqlSourceRoots = []string{
	"internal/db/queries/",
	"internal/store/migrations/",
}

type importBoundary struct {
	pathPrefix string
	forbidden  map[string]bool
}

var productionImportBoundaries = []importBoundary{
	{
		pathPrefix: "internal/query/",
		forbidden: map[string]bool{
			"github.com/islishude/etherview/internal/enrich":  true,
			"github.com/islishude/etherview/internal/httpapi": true,
		},
	},
	{
		pathPrefix: "internal/state/",
		forbidden: map[string]bool{
			"github.com/islishude/etherview/internal/httpapi": true,
		},
	},
	{
		pathPrefix: "internal/publicquery/",
		forbidden: map[string]bool{
			"github.com/islishude/etherview/internal/enrich":  true,
			"github.com/islishude/etherview/internal/httpapi": true,
			"github.com/islishude/etherview/internal/query":   true,
			"github.com/islishude/etherview/internal/state":   true,
		},
	},
	{
		pathPrefix: "internal/abicalldata/",
		forbidden: map[string]bool{
			"github.com/islishude/etherview/internal/enrich": true,
		},
	},
	{
		pathPrefix: "internal/abicontract/",
		forbidden: map[string]bool{
			"github.com/islishude/etherview/internal/enrich": true,
		},
	},
	{
		pathPrefix: "internal/proxycontract/",
		forbidden: map[string]bool{
			"github.com/islishude/etherview/internal/enrich": true,
		},
	},
	{
		pathPrefix: "internal/stagecontract/",
		forbidden: map[string]bool{
			"github.com/islishude/etherview/internal/enrich": true,
		},
	},
}

// Diagnostic is one deterministic source-boundary failure.
type Diagnostic struct {
	Path    string
	Line    int
	Message string
}

func (diagnostic Diagnostic) String() string {
	if diagnostic.Line > 0 {
		return fmt.Sprintf("%s:%d: %s", diagnostic.Path, diagnostic.Line, diagnostic.Message)
	}
	return fmt.Sprintf("%s: %s", diagnostic.Path, diagnostic.Message)
}

// Report is the complete source-boundary result.
type Report struct {
	Diagnostics []Diagnostic
	GoFiles     int
	SQLFiles    int
}

func (report Report) OK() bool { return len(report.Diagnostics) == 0 }

// Check verifies that production SQL originates in sqlc query or migration
// sources. The migration runner and validated partition module are the only Go
// files allowed to own executable raw SQL.
func Check(root string) Report {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return Report{Diagnostics: []Diagnostic{{Path: root, Message: err.Error()}}}
	}
	set := token.NewFileSet()
	report := Report{}
	err = filepath.WalkDir(absoluteRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(absoluteRoot, path)
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
			report.SQLFiles++
			if !hasAllowedPrefix(relative, sqlSourceRoots) {
				report.Diagnostics = append(report.Diagnostics, Diagnostic{
					Path: relative, Message: "SQL source must live under internal/db/queries or internal/store/migrations",
				})
			}
			return nil
		}
		if !strings.HasSuffix(relative, ".go") {
			return nil
		}
		report.GoFiles++
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		lines := bytes.Count(content, []byte{'\n'})
		if len(content) > 0 && content[len(content)-1] != '\n' {
			lines++
		}
		limit := maximumProductionGoLines
		kind := "production"
		if strings.HasSuffix(relative, "_test.go") {
			limit = maximumTestGoLines
			kind = "test"
		}
		if lines > limit {
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Path: relative,
				Message: fmt.Sprintf(
					"hand-written %s Go file has %d lines; maximum is %d",
					kind, lines, limit,
				),
			})
		}
		if kind == "test" {
			return nil
		}
		file, err := parser.ParseFile(set, path, content, 0)
		if err != nil {
			return err
		}
		checkImportBoundaries(set, &report, relative, file)
		if rawSQLExecutors[relative] {
			return nil
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
			position := set.Position(literal.Pos())
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Path: relative, Line: position.Line,
				Message: "production SQL must originate in internal/db/queries; use a named sqlc query",
			})
			return true
		})
		return nil
	})
	if err != nil {
		report.Diagnostics = append(report.Diagnostics, Diagnostic{Path: root, Message: err.Error()})
	}
	sort.Slice(report.Diagnostics, func(left, right int) bool {
		if report.Diagnostics[left].Path == report.Diagnostics[right].Path {
			return report.Diagnostics[left].Line < report.Diagnostics[right].Line
		}
		return report.Diagnostics[left].Path < report.Diagnostics[right].Path
	})
	return report
}

func checkImportBoundaries(
	set *token.FileSet,
	report *Report,
	path string,
	file *ast.File,
) {
	for _, boundary := range productionImportBoundaries {
		if !strings.HasPrefix(path, boundary.pathPrefix) {
			continue
		}
		for _, imported := range file.Imports {
			value, err := strconv.Unquote(imported.Path.Value)
			if err != nil || !boundary.forbidden[value] {
				continue
			}
			position := set.Position(imported.Pos())
			report.Diagnostics = append(report.Diagnostics, Diagnostic{
				Path: path, Line: position.Line,
				Message: fmt.Sprintf(
					"package boundary forbids importing %s", value,
				),
			})
		}
	}
}

func looksLikeSQL(value string) bool {
	trimmed := strings.TrimSpace(value)
	return selectStatement.MatchString(trimmed) || withStatement.MatchString(trimmed) ||
		dmlStatement.MatchString(trimmed) || ddlStatement.MatchString(trimmed) ||
		transactionSQL.MatchString(trimmed) || queryFragment.MatchString(trimmed)
}

func hasAllowedPrefix(path string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
