package testpgx

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
)

var statementCache struct {
	sync.Once
	values map[string]string
}

// Statement exposes generated SQL only to source-contract tests. Production
// imports of testpgx are rejected by source-check; no SQL execution or parameter
// conversion lives here. Repository fakes continue to receive native pgx values.
func Statement(name string) string {
	statementCache.Do(func() {
		_, source, _, ok := runtime.Caller(0)
		if !ok {
			panic("locate generated SQL test fixture")
		}
		paths, err := filepath.Glob(filepath.Join(filepath.Dir(source), "..", "db", "gen", "*.sql.go"))
		if err != nil {
			panic(err)
		}
		statementCache.values = make(map[string]string)
		set := token.NewFileSet()
		for _, path := range paths {
			raw, err := os.ReadFile(path)
			if err != nil {
				panic(err)
			}
			file, err := parser.ParseFile(set, path, raw, 0)
			if err != nil {
				panic(err)
			}
			ast.Inspect(file, func(node ast.Node) bool {
				literal, ok := node.(*ast.BasicLit)
				if !ok || literal.Kind != token.STRING {
					return true
				}
				value, err := strconv.Unquote(literal.Value)
				if err != nil || !strings.HasPrefix(value, "-- name: ") {
					return true
				}
				fields := strings.Fields(value)
				if len(fields) >= 3 {
					statementCache.values[fields[2]] = value
				}
				return true
			})
		}
	})
	value, ok := statementCache.values[name]
	if !ok {
		panic("missing generated SQL statement: " + name)
	}
	return value
}
