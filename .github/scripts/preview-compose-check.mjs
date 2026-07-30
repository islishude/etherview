import fs from "node:fs";

const config = JSON.parse(fs.readFileSync(0, "utf8"));
const services = config.services ?? {};
const expectedRunner = process.env.ETHERVIEW_PREVIEW_COMPILER_RUNNER_IMAGE;
const compilerNetwork = "compiler-runtime";
const roles = ["api", "sync", "enrich", "trace", "verify", "metadata", "maintenance"];
const runtimeImage =
  "docker:29.6.2-dind@sha256:bfec1f5159c63a81ca6fdedbd81404d2c0e16378ed0feec3bb3fbf3998847659";
const clientImage =
  "docker:29.6.2-cli@sha256:be132a9f282288de4afaf63379dff75711fda0147c6b72a9df44e51841402144";

function fail(message) {
  throw new Error(`Preview compiler boundary: ${message}`);
}

function requireService(name) {
  const service = services[name];
  if (!service) fail(`missing service ${name}`);
  return service;
}

function networkNames(service) {
  return Object.keys(service.networks ?? {});
}

function hasOnlyNetworks(service, expected) {
  const actual = networkNames(service).sort();
  const wanted = [...expected].sort();
  return actual.length === wanted.length && actual.every((name, index) => name === wanted[index]);
}

function volumeAt(service, target) {
  return (service.volumes ?? []).find((volume) => volume.target === target);
}

if (!/^etherview-compiler-runner@sha256:[0-9a-f]{64}$/.test(expectedRunner ?? "")) {
  fail("render check requires an exact digest-pinned runner reference");
}

const runtime = requireService("compiler-runtime");
if (runtime.image !== runtimeImage) fail(`unexpected nested daemon image ${runtime.image}`);
if (runtime.privileged !== true) fail("nested daemon must be privileged so compiler cgroup limits are enforced");
if ((runtime.ports ?? []).length !== 0) fail("nested daemon must not publish a host port");
if (!hasOnlyNetworks(runtime, [compilerNetwork])) fail("nested daemon must use only its private network");
const runtimeData = volumeAt(runtime, "/var/lib/docker");
if (!runtimeData || runtimeData.type !== "volume") fail("nested daemon state must use a named volume");

const volumeInit = requireService("compiler-volumes-init");
if (volumeInit.image !== clientImage) fail(`unexpected volume init image ${volumeInit.image}`);
if (volumeInit.network_mode !== "none") fail("compiler volume init must be networkless");
if (volumeInit.privileged === true || volumeInit.read_only !== true) {
  fail("compiler volume init must be unprivileged with a read-only root filesystem");
}
if (
  (volumeInit.cap_add ?? []).length !== 1 ||
  volumeInit.cap_add[0] !== "CHOWN" ||
  !(volumeInit.cap_drop ?? []).includes("ALL")
) {
  fail("compiler volume init must retain only CAP_CHOWN");
}
if (!(volumeInit.security_opt ?? []).includes("no-new-privileges:true")) {
  fail("compiler volume init must forbid privilege escalation");
}
for (const target of ["/runtime-client", "/compiler-cache"]) {
  const volume = volumeAt(volumeInit, target);
  if (!volume || volume.type !== "volume" || volume.read_only === true) {
    fail(`compiler volume init requires writable named volume ${target}`);
  }
}
const volumeInitCommand = (volumeInit.command ?? []).join("\n");
if (
  !volumeInitCommand.includes("chmod 0750") ||
  !volumeInitCommand.includes("chown 65532:65532")
) {
  fail("compiler volume init must install the production non-root ownership");
}

const preflight = requireService("compiler-preflight");
if (preflight.image !== clientImage) fail(`unexpected preflight client image ${preflight.image}`);
if (preflight.privileged === true) fail("compiler preflight must not be privileged");
if (preflight.read_only !== true) fail("compiler preflight root filesystem must be read-only");
if (!(preflight.cap_drop ?? []).includes("ALL")) fail("compiler preflight must drop all capabilities");
if (!(preflight.security_opt ?? []).includes("no-new-privileges:true")) {
  fail("compiler preflight must forbid privilege escalation");
}
if (preflight.user !== "65532:65532") fail("compiler preflight must run as the production non-root identity");
if ((preflight.ports ?? []).length !== 0) fail("compiler preflight must not publish a host port");
if (!hasOnlyNetworks(preflight, [compilerNetwork])) fail("compiler preflight must use only its private network");
if (preflight.environment?.DOCKER_HOST !== "tcp://compiler-runtime:2375") {
  fail("compiler preflight must target only the nested daemon");
}
if (preflight.environment?.ETHERVIEW_VERIFICATION_RUNNER_IMAGE !== expectedRunner) {
  fail("compiler preflight did not receive the exact runner reference");
}
if (preflight.depends_on?.["compiler-runtime"]?.condition !== "service_healthy") {
  fail("compiler preflight must wait for the nested daemon health check");
}
if (preflight.depends_on?.["compiler-volumes-init"]?.condition !== "service_completed_successfully") {
  fail("compiler preflight must wait for non-root volume ownership");
}
const preflightCommand = (preflight.command ?? []).join("\n");
for (const required of [
  "cp /usr/local/bin/docker /runtime-client/docker",
  'probe=/compiler-cache/.preview-write-probe',
  "docker image load --input /runtime/compiler-runner.tar",
  'docker image inspect "$${ETHERVIEW_VERIFICATION_RUNNER_IMAGE}"',
  "'{{.Os}}/{{.Architecture}}'",
  "= linux/amd64",
  "docker create --pull=never --platform=linux/amd64",
  "--network=none --read-only --cap-drop=ALL",
  "--security-opt=no-new-privileges --user=65532:65532",
  "--memory=512m --memory-swap=512m --cpus=1 --pids-limit=64",
  "--ulimit=nofile=64:64 --ulimit=core=0",
  "--tmpfs=/tmp:rw,exec,nosuid,nodev,size=272m,mode=0700",
  'docker start --attach "$${smoke}"',
  'test "$${smoke_status}" -eq 1',
  "'{{.State.Error}}'",
]) {
  if (!preflightCommand.includes(required)) fail(`compiler preflight command is missing ${required}`);
}
const archive = volumeAt(preflight, "/runtime/compiler-runner.tar");
if (!archive || archive.type !== "bind" || archive.read_only !== true) {
  fail("compiler preflight runner archive must be a read-only bind");
}
if (!archive.source?.endsWith("/.local/preview-compiler/compiler-runner.tar")) {
  fail("compiler preflight runner archive drifted from the Make-owned local path");
}
if (
  !(preflight.tmpfs ?? []).some(
    (value) =>
      value.includes("/tmp:") &&
      value.includes("size=1m") &&
      value.includes("uid=65532") &&
      value.includes("gid=65532"),
  )
) {
  fail("compiler preflight tmpfs must belong to the production non-root identity");
}
const writableClient = volumeAt(preflight, "/runtime-client");
if (!writableClient || writableClient.type !== "volume" || writableClient.read_only === true) {
  fail("compiler preflight must populate a named client volume");
}
const writableCache = volumeAt(preflight, "/compiler-cache");
if (!writableCache || writableCache.type !== "volume" || writableCache.read_only === true) {
  fail("compiler preflight must prove the non-root compiler cache is writable");
}

const applicationImage = requireService("api").image;
for (const role of roles) {
  const service = requireService(role);
  if (service.image !== applicationImage) fail(`${role} does not use the shared production image`);
  if (!Object.hasOwn(service.environment ?? {}, "ETHERVIEW_METADATA_IPFS_GATEWAY")) {
    fail(`metadata gateway override is missing from ${role}`);
  }
  const hasRunner = Object.hasOwn(service.environment ?? {}, "ETHERVIEW_VERIFICATION_RUNNER_IMAGE");
  if (hasRunner !== (role === "api" || role === "verify")) {
    fail(`runner identity has the wrong role scope on ${role}`);
  }
  const hasDaemon = Object.hasOwn(service.environment ?? {}, "DOCKER_HOST");
  if (hasDaemon !== (role === "verify")) fail(`container runtime has the wrong role scope on ${role}`);
  const hasUnsafePrivateDownloads = Object.hasOwn(
    service.environment ?? {},
    "ETHERVIEW_VERIFICATION_UNSAFE_ALLOW_PRIVATE_DOWNLOAD_NETWORKS",
  );
  if (hasUnsafePrivateDownloads !== (role === "verify")) {
    fail(`unsafe private verification downloads have the wrong role scope on ${role}`);
  }
  if (networkNames(service).includes(compilerNetwork) !== (role === "verify")) {
    fail(`compiler network has the wrong role scope on ${role}`);
  }
}

const api = requireService("api");
if (api.environment.ETHERVIEW_VERIFICATION_RUNNER_IMAGE !== expectedRunner) {
  fail("API did not receive the exact runner reference used for provenance");
}

const verify = requireService("verify");
if (!hasOnlyNetworks(verify, ["default", compilerNetwork])) {
  fail("verify must use only the application and compiler networks");
}
if (verify.networks.default?.gw_priority !== 1) {
  fail("verify application network must remain its preferred egress route");
}
if ((verify.networks[compilerNetwork]?.gw_priority ?? 0) !== 0) {
  fail("verify compiler network must not become its egress route");
}
if (verify.environment?.DOCKER_HOST !== "tcp://compiler-runtime:2375") {
  fail("verify must target only the nested daemon");
}
if (verify.environment?.ETHERVIEW_VERIFICATION_RUNNER_IMAGE !== expectedRunner) {
  fail("verify did not receive the exact runner reference");
}
if (verify.environment?.ETHERVIEW_VERIFICATION_UNSAFE_ALLOW_PRIVATE_DOWNLOAD_NETWORKS !== "true") {
  fail("verify must explicitly enable its Preview-only fake-IP download exception");
}
if (!verify.environment?.PATH?.split(":").includes("/runtime-client")) {
  fail("verify PATH does not contain the isolated runtime client");
}
if (verify.depends_on?.["compiler-preflight"]?.condition !== "service_completed_successfully") {
  fail("verify must wait for successful compiler preflight");
}
const readonlyClient = volumeAt(verify, "/runtime-client");
if (!readonlyClient || readonlyClient.type !== "volume" || readonlyClient.read_only !== true) {
  fail("verify runtime client volume must be read-only");
}

for (const [name, service] of Object.entries(services)) {
  if (name !== "compiler-runtime" && service.privileged === true) {
    fail(`unexpected privileged service ${name}`);
  }
  if (name !== "compiler-volumes-init" && (service.cap_add ?? []).length !== 0) {
    fail(`unexpected added capability on ${name}`);
  }
  for (const volume of service.volumes ?? []) {
    if (`${volume.source ?? ""}:${volume.target ?? ""}`.includes("/var/run/docker.sock")) {
      fail(`host Docker socket leaked to ${name}`);
    }
  }
}

if (config.networks?.[compilerNetwork]?.internal !== true) {
  fail("compiler runtime network must be internal");
}
