import { gunzipSync, gzipSync } from "node:zlib";

// Archive libraries parse untrusted inputs; policy and resource bounds live here.
import AdmZip from "adm-zip";
import tar from "tar-stream";

import { MAX_ARCHIVE, safeMember } from "./tooling.mjs";
export function zipEntries(raw) {
  if (raw.length > MAX_ARCHIVE) throw new Error("oversized ZIP");
  const entries = new AdmZip(raw).getEntries();
  const names = new Set();
  let total = 0;
  for (const entry of entries) {
    const name = entry.entryName;
    safeMember(entry.isDirectory ? name.slice(0, -1) : name);
    if (names.has(name)) throw new Error("duplicate archive member");
    names.add(name);
    total += entry.header.size;
    if (
      total > MAX_ARCHIVE ||
      ((entry.attr >>> 16) & 0o170000) === 0o120000 ||
      entry.header.encrypted
    ) {
      throw new Error("unsafe wheel");
    }
  }
  return entries;
}
export function makeZip(files) {
  const archive = new AdmZip({ noSort: true });
  for (const [name, value] of [...files].sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0))) {
    safeMember(name);
    archive.addFile(name, value);
    const entry = archive.getEntry(name);
    entry.header.timeval = 0x00210000; // 1980-01-01, independent of host TZ.
    entry.header.made = 0x0314;
    entry.header.attr = 0o100444 * 65536;
  }
  return archive.toBuffer();
}
export async function sourceEntries(raw) {
  const uncompressed = gunzipSync(raw, { maxOutputLength: MAX_ARCHIVE });
  const extractor = tar.extract();
  const result = new Map();
  let total = 0;
  const consume = (async () => {
    for await (const stream of extractor) {
      const { name, type, size } = stream.header;
      if (type === "directory") {
        safeMember(name.replace(/\/$/, ""));
        stream.resume();
        continue;
      }
      if (type !== "file") throw new Error("unsafe source archive");
      safeMember(name);
      total += size;
      if (total > MAX_ARCHIVE || result.has(name))
        throw new Error("oversized or duplicate source archive");
      const chunks = [];
      for await (const chunk of stream) chunks.push(chunk);
      result.set(name, Buffer.concat(chunks));
    }
    return result;
  })();
  extractor.end(uncompressed);
  return consume;
}
export async function makeTarGzip(files) {
  const pack = tar.pack();
  const consume = (async () => {
    const chunks = [];
    for await (const chunk of pack) chunks.push(chunk);
    return Buffer.concat(chunks);
  })();
  for (const [name, data] of files) {
    safeMember(name);
    await new Promise((resolve, reject) =>
      pack.entry(
        { name, mode: 0o444, uid: 0, gid: 0, mtime: new Date(0), type: "file" },
        data,
        (e) => (e ? reject(e) : resolve()),
      ),
    );
  }
  pack.finalize();
  const raw = await consume;
  // Match tarfile's 10 KiB record framing, accepted by the Go extractor.
  const padded = Buffer.concat([raw, Buffer.alloc((10240 - (raw.length % 10240)) % 10240)]);
  const compressed = gzipSync(padded, { level: 9 });
  compressed[9] = 255; // No host-specific gzip OS byte.
  return compressed;
}
