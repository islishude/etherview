import { spawnSync } from "node:child_process";
import { existsSync, readFileSync } from "node:fs";
import { dirname, extname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

function prose(text) {
  let fence;
  return text
    .split("\n")
    .map((line) => {
      const marker = line.match(/^\s{0,3}(`{3,}|~{3,})/u)?.[1];
      if (fence) {
        if (marker?.[0] === fence[0] && marker.length >= fence.length) fence = undefined;
        return "";
      }
      if (marker) {
        fence = marker;
        return "";
      }
      return line;
    })
    .join("\n");
}

function anchors(text) {
  const result = new Set();
  for (const match of prose(text).matchAll(/^#{1,6}\s+(.+?)\s*#*$/gmu)) {
    const base = match[1]
      .toLowerCase()
      .replace(/<[^>]*>/gu, "")
      .replace(/[^\p{L}\p{N}\p{M}_ -]/gu, "")
      .replace(/ /gu, "-");
    let anchor = base;
    for (let suffix = 1; result.has(anchor); suffix++) anchor = `${base}-${suffix}`;
    result.add(anchor);
  }
  for (const match of prose(text).matchAll(
    /<(?:a|[a-z][a-z0-9]*)\b[^>]*\b(?:id|name)=["']([^"']+)["']/giu,
  )) {
    result.add(match[1]);
  }
  return result;
}

// Repository documentation uses ATX headings, inline links and reference links.
// Ignore fenced examples and inline code; check only local files and Markdown anchors.
export function checkDocumentationLinks(root, files) {
  const failures = [];
  const cache = new Map();
  let links = 0;
  for (const file of files) {
    const source = resolve(root, file);
    const text = prose(readFileSync(source, "utf8")).replace(/(`+)[\s\S]*?\1/gu, "");
    const definitions = new Map(
      [...text.matchAll(/^\s{0,3}\[([^\]]+)\]:\s*(<[^>]+>|\S+)/gmu)].map((m) => [
        m[1].toLowerCase(),
        m[2],
      ]),
    );
    const destinations = [
      ...text.matchAll(/\]\((<[^>]+>|(?:[^\s()]|\([^()]*\))+)(?:\s+["'][^\n]*["'])?\)/gu),
    ].map((m) => m[1]);
    for (const match of text.matchAll(/\[([^\]\n]+)\]\[([^\]\n]*)\]/gu)) {
      const name = (match[2] || match[1]).toLowerCase();
      const destination = definitions.get(name);
      if (destination) destinations.push(destination);
      else failures.push(`${file}: undefined link reference [${name}]`);
    }
    // Definition targets also cover shortcut reference links.
    destinations.push(...definitions.values());
    for (let destination of destinations) {
      destination = destination.replace(/^<|>$/gu, "");
      if (/^(?:[a-z][a-z0-9+.-]*:|\/\/)/iu.test(destination)) continue;
      links++;
      let path, fragment;
      try {
        const hash = destination.indexOf("#");
        path = decodeURIComponent(
          (hash < 0 ? destination : destination.slice(0, hash)).split("?")[0],
        );
        fragment = hash < 0 ? "" : decodeURIComponent(destination.slice(hash + 1));
      } catch {
        failures.push(`${file}: invalid local URL ${destination}`);
        continue;
      }
      const target = path
        ? resolve(path.startsWith("/") ? root : dirname(source), path.replace(/^\//u, ""))
        : source;
      if (!existsSync(target)) {
        failures.push(`${file}: missing local target ${destination}`);
      } else if (fragment && extname(target) === ".md") {
        if (!cache.has(target)) cache.set(target, anchors(readFileSync(target, "utf8")));
        if (!cache.get(target).has(fragment))
          failures.push(`${file}: missing anchor ${destination}`);
      }
    }
  }
  return { failures, links };
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const listing = spawnSync(
    "git",
    ["ls-files", "--cached", "--others", "--exclude-standard", "-z", "--", "*.md"],
    { encoding: "utf8" },
  );
  if (listing.error || listing.status !== 0)
    throw new Error("Cannot enumerate repository Markdown files");
  const files = [
    ...new Set(
      listing.stdout
        .split("\0")
        .filter(
          (file) =>
            file &&
            existsSync(file) &&
            !file.startsWith("licenses/") &&
            !file.includes("/testdata/"),
        ),
    ),
  ];
  const { failures, links } = checkDocumentationLinks(process.cwd(), files);
  for (const failure of failures) console.error(`docs-links: ${failure}`);
  if (failures.length) process.exitCode = 1;
  else console.log(`docs-links: ok (${files.length} documents, ${links} local links)`);
}
