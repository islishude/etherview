import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { copyFileSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join, relative, resolve } from "node:path";
import test from "node:test";

const repository = resolve(import.meta.dirname, "../..");
const modulePath = "github.com/islishude/etherview";

test("real golangci-lint enforces database, package and file-length rules with the repository config", () => {
  const root = mkdtempSync(join(tmpdir(), "etherview lint policy "));
  const expected = [];
  const write = (path, source) => {
    mkdirSync(dirname(join(root, path)), { recursive: true });
    writeFileSync(join(root, path), source);
    source.split("\n").forEach((line, index) => {
      const want = line.match(/\/\/ want (depguard|forbidigo)$/);
      if (want) expected.push(`${path}:${index + 1}:${want[1]}`);
    });
  };
  try {
    for (const path of ["go.mod", "go.sum", ".golangci.yml"]) {
      copyFileSync(join(repository, path), join(root, path));
    }
    // pq is deliberately absent from production dependencies. Only its import
    // path matters to depguard; native pgx below uses the real pinned module.
    write("pq/go.mod", "module github.com/lib/pq\n\ngo 1.27.3\n");
    write("pq/pq.go", "package pq\n");
    write(
      "go.mod",
      `${readFileSync(join(root, "go.mod"), "utf8")}\nrequire github.com/lib/pq v0.0.0\nreplace github.com/lib/pq => ./pq\n`,
    );
    for (const name of [
      "httpapi",
      "enrich",
      "enssync",
      "ens",
      "ethrpc",
      "query",
      "state",
      "testpgx",
    ]) {
      write(`internal/${name}/stub.go`, `package ${name}\n`);
    }

    const boundaries = {
      query: ["httpapi", "enrich", "enssync"],
      state: ["httpapi"],
      publicquery: ["httpapi", "enrich", "enssync", "query", "state"],
      abicalldata: ["enrich", "enssync"],
      abicontract: ["enrich", "enssync"],
      proxycontract: ["enrich", "enssync"],
      stagecontract: ["enrich", "enssync"],
      enscontract: ["enrich", "enssync", "ens", "ethrpc", "db", "httpapi"],
    };
    for (const [owner, forbidden] of Object.entries(boundaries)) {
      for (const dependency of forbidden) {
        const imports = `import _ "${modulePath}/internal/${dependency}"`;
        write(
          `internal/${owner}/bad_${dependency}.go`,
          `package ${owner}\n${imports} // want depguard\n`,
        );
        // Subdirectories also belong to the owner's boundary. Test imports
        // retain the existing exemption without exempting database drivers.
        write(
          `internal/${owner}/${dependency}/bad.go`,
          `package fixture\n${imports} // want depguard\n`,
        );
        write(`internal/${owner}/${dependency}/allowed_test.go`, `package fixture\n${imports}\n`);
      }
    }
    for (const [index, dependency] of [
      "database/sql",
      "database/sql/driver",
      "github.com/jackc/pgx/v5/stdlib",
      "github.com/lib/pq",
    ].entries()) {
      for (const suffix of [".go", "_test.go"]) {
        write(
          `internal/drivers/driver${index}${suffix}`,
          `package drivers\nimport _ "${dependency}" // want depguard\n`,
        );
      }
    }
    write(
      "internal/fixtures/bad.go",
      `package fixtures\nimport _ "${modulePath}/internal/testpgx" // want depguard\n`,
    );
    write(
      "internal/fixtures/allowed_test.go",
      `package fixtures\nimport _ "${modulePath}/internal/testpgx"\n`,
    );
    write("internal/state/allowed.go", `package state\nimport _ "${modulePath}/internal/enrich"\n`);
    write(
      "internal/consumer/allowed.go",
      `package consumer\nimport _ "${modulePath}/internal/httpapi"\n`,
    );

    write("internal/db/gen/db.go", readFileSync(join(repository, "internal/db/gen/db.go"), "utf8"));
    write(
      "internal/db/runtime.go",
      `package dbaccess
import "${modulePath}/internal/db/gen"
type Database interface { dbgen.DBTX }
`,
    );
    const raw = `package fixture
import (
  "context"
  "net/url"
  native "github.com/jackc/pgx/v5"
  "github.com/jackc/pgx/v5/pgxpool"
  "github.com/jackc/pgx/v5/pgconn"
  dbaccess "${modulePath}/internal/db"
  "${modulePath}/internal/db/gen"
)
type Alias = native.Conn
type search struct{}
func (search) Query(context.Context, string) {}
func calls(ctx context.Context, statement string, pool *pgxpool.Pool, conn *Alias, tx native.Tx, db dbgen.DBTX, database dbaccess.Database, wire *pgconn.PgConn) {
  _, _ = pool.Exec(ctx, statement) // want forbidigo
  _, _ = conn.Query(ctx, statement) // want forbidigo
  _ = tx.QueryRow(ctx, statement) // want forbidigo
  _, _ = conn.Prepare(ctx, "name", statement) // want forbidigo
  _ = conn.SendBatch(ctx, &native.Batch{}) // want forbidigo
  _, _ = conn.CopyFrom(ctx, nil, nil, nil) // want forbidigo
  _, _ = db.Query(ctx, statement) // want forbidigo
  _, _ = database.Exec(ctx, statement) // want forbidigo
  _ = pool.Query // want forbidigo
  _ = (*native.Conn).Exec // want forbidigo
  _ = wire.Exec(ctx, statement) // want forbidigo
  _ = (&url.URL{}).Query()
  search{}.Query(ctx, statement)
  _ = dbgen.New(pool).WithTx(tx)
}
`;
    write("internal/sample/bad.go", raw);
    const allowed = raw.replaceAll(" // want forbidigo", "");
    write("internal/testonly/allowed_test.go", allowed);
    write("internal/store/migrate.go", allowed.replace("func calls(", "func migrate("));
    // A separate package avoids duplicate declarations while testing the exact
    // partition exemption and a similarly named file outside store.
    write(
      "internal/store/partition.go",
      `package fixture
import ("context"; "github.com/jackc/pgx/v5")
func partition(ctx context.Context, tx pgx.Tx, statement string) { _, _ = tx.Exec(ctx, statement) }
`,
    );
    write("internal/other/migrate.go", raw);
    write("internal/generated/query.go", `// Code generated by sqlc. DO NOT EDIT.\n${allowed}`);
    write("internal/tagged/bad.go", `//go:build integration\n\n${raw}`);
    write(
      "internal/maintenance/session.go",
      `package maintenance
import (
  "context"
  "${modulePath}/internal/db/gen"
  "github.com/jackc/pgx/v5/pgxpool"
)
type sessionConnection interface { dbgen.DBTX }
type nativeSession struct { *pgxpool.Conn }
func calls(ctx context.Context, statement string, conn sessionConnection, session nativeSession) {
  _, _ = conn.Exec(ctx, statement) // want forbidigo
  _, _ = session.Query(ctx, statement) // want forbidigo
}
`,
    );

    // Count physical lines, including comments and blanks, with or without a
    // final newline. Production and test files share the same exact boundary.
    for (const suffix of [".go", "_test.go"]) {
      for (const length of [2499, 2500, 2501]) {
        for (const newline of [false, true]) {
          const lines = ["package length"];
          while (lines.length < length - 1) lines.push(lines.length % 2 ? "// comment" : "");
          lines.push("// last line");
          const source = lines.join("\n") + (newline ? "\n" : "");
          const path = `internal/length/lines_${length}_${newline}${suffix}`;
          write(path, source);
          if (length > 2500) expected.push(`${path}:${length}:revive`);
        }
      }
    }
    write(
      "internal/length/generated.go",
      `// Code generated by fixture. DO NOT EDIT.\npackage length\n${"// comment\n".repeat(2500)}`,
    );

    const report = join(root, "issues.json");
    const configured = spawnSync(
      process.env.GOLANGCI_LINT ?? "golangci-lint",
      ["linters", "--config", join(root, ".golangci.yml"), "--json"],
      { cwd: root, env: { ...process.env, GOWORK: "off" }, encoding: "utf8", timeout: 30_000 },
    );
    assert.ifError(configured.error);
    assert.equal(configured.status, 0, configured.stderr);
    const enabled = new Set(JSON.parse(configured.stdout).Enabled.map(({ name }) => name));
    for (const name of ["depguard", "forbidigo", "revive"])
      assert(enabled.has(name), `${name} must be enabled in ordinary lint runs`);
    const result = spawnSync(
      process.env.GOLANGCI_LINT ?? "golangci-lint",
      [
        "run",
        "--config",
        join(root, ".golangci.yml"),
        "--enable-only=depguard,forbidigo,revive",
        "--output.text.path=",
        `--output.json.path=${report}`,
        "./...",
      ],
      { cwd: root, env: { ...process.env, GOWORK: "off" }, encoding: "utf8", timeout: 180_000 },
    );
    assert.ifError(result.error);
    assert.equal(result.status, 1, `${result.stdout}\n${result.stderr}`);
    const { Issues: issues } = JSON.parse(readFileSync(report, "utf8"));
    assert.deepEqual(
      issues
        .map(({ FromLinter, Pos }) => {
          const path = relative(root, resolve(root, Pos.Filename)).replaceAll("\\", "/");
          return `${path}:${Pos.Line}:${FromLinter}`;
        })
        .sort(),
      expected.sort(),
      `${result.stdout}\n${result.stderr}`,
    );
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
