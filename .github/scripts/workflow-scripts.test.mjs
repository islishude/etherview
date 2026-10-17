import test from "node:test";
import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const root = resolve(import.meta.dirname, "../..");
function sandbox(fn) {
  const dir = mkdtempSync(join(tmpdir(), "workflow scripts "));
  const bin = join(dir, "bin"), log = join(dir, "commands.jsonl");
  mkdirSync(bin);
  const env = { ...process.env, PATH: `${bin}:${process.env.PATH}`, RUNNER_TEMP: dir, COMMAND_LOG: log };
  const fake = (name) => writeFileSync(join(bin, name), `#!${process.execPath}
import {appendFileSync} from 'node:fs';
appendFileSync(process.env.COMMAND_LOG, JSON.stringify({command:${JSON.stringify(name)},args:process.argv.slice(2)})+'\\n');
if (${JSON.stringify(name)} === process.env.FAIL_COMMAND) process.exit(9);
if (${JSON.stringify(name)} === 'node' && process.argv.includes('--inspect')) console.log('create');
`, { mode: 0o755 });
  const run = (script, args = [], extra = {}) => spawnSync("bash", [join(root, ".github/scripts", script), ...args], { cwd: root, env: { ...env, ...extra }, encoding: "utf8" });
  const commands = () => existsSync(log) ? readFileSync(log, "utf8").trim().split("\n").map(JSON.parse) : [];
  try { fn({ dir, env, fake, run, commands }); }
  finally { rmSync(dir, { recursive: true, force: true }); }
}

test("image metadata uses exact release tag or commit and appends Actions outputs", () => sandbox(({ dir, env }) => {
  const git = (...args) => execFileSync("git", args, { cwd: dir, encoding: "utf8" }).trim();
  git("init", "-q");
  git("-c", "user.name=Test", "-c", "user.email=test@localhost", "-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null", "commit", "--allow-empty", "-qm", "test");
  const sha = git("rev-parse", "HEAD"), output = join(dir, "output");
  for (const tagged of [false, true]) {
    if (tagged) git("tag", "v1.2.3");
    writeFileSync(output, "existing=value\n");
    execFileSync("bash", [join(root, ".github/scripts/docker-image-metadata.sh")], { cwd: dir, env: { ...env, GITHUB_SHA: sha, GITHUB_OUTPUT: output } });
    assert.equal(readFileSync(output, "utf8"), `existing=value\nversion=${tagged ? "v1.2.3" : git("rev-parse", "--short", sha)}\nrevision=${sha}\ncreated=${git("show", "-s", "--format=%cI", sha)}\n`);
  }
}));

test("digest export accepts exact hashes and rejects malformed artifacts", () => sandbox(({ dir, run }) => {
  assert.notEqual(run("docker-export-digest.sh", [], { IMAGE_DIGEST: "sha256:../../bad" }).status, 0);
  assert(!existsSync(join(dir, "digests")));
  for (const digest of ["a".repeat(64), "b".repeat(64)]) {
    assert.equal(run("docker-export-digest.sh", [], { IMAGE_DIGEST: `sha256:${digest}` }).status, 0);
    assert(existsSync(join(dir, "digests", digest)));
  }
}));

test("Vyper PR wrapper retains manual flag and paths with spaces", () => sandbox(({ dir, fake, run, commands }) => {
  fake("node");
  const output = join(dir, "output"), env = { VERSION: "1.2.3", GITHUB_OUTPUT: output, MANUAL: "true" };
  assert.equal(run("vyper-update-pr.sh", ["inspect"], env).status, 0);
  assert.equal(readFileSync(output, "utf8"), "action=create\n");
  assert.equal(run("vyper-update-pr.sh", ["publish"], env).status, 0);
  assert.deepEqual(commands().at(-1).args, ["compiler/vyper/update-pr.mjs", "--artifact", join(dir, "vyper-update"), "--version", "1.2.3", "--manual"]);
  assert.equal(run("vyper-update-pr.sh", ["inspect"], { ...env, MANUAL: "false" }).status, 0);
  assert(!commands().at(-1).args.includes("--manual"));
  const before = readFileSync(output, "utf8");
  assert.notEqual(run("vyper-update-pr.sh", ["inspect"], { ...env, FAIL_COMMAND: "node" }).status, 0);
  assert.equal(readFileSync(output, "utf8"), before);
}));

test("CI command groups preserve sequence and stop after the first failure", () => {
  for (const [script, expected] of [
    ["vyper-ci-packages.sh", ["npm", "node", "node", "node"]],
    ["vyper-ci-native.sh", ["npm", "node", "npm"]],
    ["preview-ci-certificates.sh", ["make", "sudo", "chmod"]],
  ]) sandbox(({ fake, run, commands }) => {
    for (const command of new Set(expected)) fake(command);
    assert.equal(run(script).status, 0);
    assert.deepEqual(commands().map(c => c.command), expected);
    assert.equal(run(script, [], { FAIL_COMMAND: expected[0] }).status, 9);
    assert.equal(commands().length, expected.length + 1);
  });
});

test("production image handoff checks revision, architecture, identity and failures", () => {
  for (const architecture of ["amd64", "arm64"]) for (const mode of ["save", "load"]) {
    for (const failure of ["", "missing", "revision", "image-id", "architecture", "os", "inspect", mode]) {
      if (mode === "save" && ["missing", "revision", "image-id"].includes(failure)) continue;
      sandbox(({ dir, env, commands }) => {
        const bundle=join(dir,"bundle"); mkdirSync(bundle);
        const imageID=`sha256:${"a".repeat(64)}`, revision="c".repeat(40);
        const metadata={schema:"etherview-production-image-v1",revision:failure==="revision"?"d".repeat(40):revision,architecture,image_id:imageID};
        if(mode==="load" && failure!=="missing") writeFileSync(join(bundle,"identity.json"),JSON.stringify(metadata));
        const identity=[{Id:failure==="image-id"?`sha256:${"b".repeat(64)}`:imageID,Architecture:failure==="architecture"?"wrong":architecture,Os:failure==="os"?"windows":"linux"}];
        writeFileSync(join(dir,"bin/docker"), `#!${process.execPath}
import {appendFileSync,writeFileSync} from 'node:fs';
const args=process.argv.slice(2);
appendFileSync(process.env.COMMAND_LOG,JSON.stringify({command:'docker',args})+'\\n');
if(args[1]===${JSON.stringify(failure)}) process.exit(9);
if(args[1]==='inspect') console.log(${JSON.stringify(JSON.stringify(identity))});
if(args[1]==='save') writeFileSync(args[3],'archive');
`,{mode:0o755});
        const result=spawnSync(process.execPath,[join(root,".github/scripts/vyper-production-bundle.mjs"),mode,bundle,architecture],{cwd:dir,env:{...env,GITHUB_SHA:revision,GITHUB_OUTPUT:join(dir,"output")},encoding:"utf8"});
        const label=`${architecture}/${mode}/${failure||"success"}`;
        if(!failure) {
          assert.equal(result.status,0,`${label}: ${result.stderr}`);
          assert.deepEqual(JSON.parse(readFileSync(join(bundle,"identity.json"))),metadata);
          if (mode === "save") assert.equal(readFileSync(join(dir,"output"),"utf8"), `identity=${JSON.stringify(metadata)}\n`);
          else assert(!existsSync(join(dir,"output")));
          assert.deepEqual(commands().map(c=>c.args[1]), mode==="save"?["inspect","save"]:["load","inspect"]);
        } else {
          assert.notEqual(result.status,0,label);
          if(mode==="save") assert(!existsSync(join(bundle,"identity.json")),label);
          if(["missing","revision"].includes(failure)) assert.equal(commands().length,0,label);
          if(mode==="load" && failure==="load") assert.equal(commands().length,1,label);
        }
      });
    }
  }
});
