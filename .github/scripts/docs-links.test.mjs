import assert from "node:assert/strict";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

import { checkDocumentationLinks } from "./docs-links.mjs";

test("documentation links resolve moved files, encoded paths, repeated headings and references", (t) => {
  const root = mkdtempSync(join(tmpdir(), "docs-links-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  writeFileSync(
    join(root, "topic file.md"),
    '# Topic\n## `Exact` identity\n## Repeat\n## Repeat\n<a id="custom"></a>\n',
  );
  writeFileSync(
    join(root, "index.md"),
    [
      "# Index",
      "[topic](topic%20file.md#exact-identity)",
      "[repeat](<topic file.md#repeat-1>)",
      "[custom][anchor]",
      "[anchor]: topic%20file.md#custom",
      "[remote](https://example.invalid/does-not-exist)",
      "`[example](absent.md)`",
      "```md",
      "[example](absent.md)",
      "```",
    ].join("\n"),
  );
  assert.deepEqual(checkDocumentationLinks(root, ["index.md"]).failures, []);
  for (const [text, message] of [
    ["[old](removed.md)", /missing local target/],
    ["[old](topic%20file.md#removed-heading)", /missing anchor/],
    ["[old][missing]", /undefined link reference/],
    ["[bad](%FF.md)", /invalid local URL/],
  ]) {
    writeFileSync(join(root, "index.md"), text);
    const { failures } = checkDocumentationLinks(root, ["index.md"]);
    assert.equal(failures.length, 1);
    assert.match(failures[0], message);
  }
});

test("example headings do not satisfy real links", (t) => {
  const root = mkdtempSync(join(tmpdir(), "docs-links-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  writeFileSync(
    join(root, "index.md"),
    '# Index\n~~~md\n## Example\n<a id="example"></a>\n~~~\n[example](#example)\n',
  );
  assert.match(checkDocumentationLinks(root, ["index.md"]).failures[0], /missing anchor/);
});
