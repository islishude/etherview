import fs from "node:fs";

const topology = process.env.ETHERVIEW_COMPOSE_TOPOLOGY;
if (topology !== "monolith" && topology !== "distributed") {
  throw new Error(`unsupported ETHERVIEW_COMPOSE_TOPOLOGY ${topology}`);
}

const document = JSON.parse(fs.readFileSync(0, "utf8"));
const services = document.services ?? {};
const apiServiceName = topology === "monolith" ? "etherview" : "api";
const apiEnvironment = services[apiServiceName]?.environment;
if (!apiEnvironment) {
  throw new Error(`missing ${apiServiceName} environment`);
}

const expected = {
  ETHERVIEW_S3_ACCESS_KEY: "compose-access",
  ETHERVIEW_S3_SECRET_KEY: "compose-secret",
  ETHERVIEW_S3_SESSION_TOKEN: "compose-session",
  AWS_ACCESS_KEY_ID: "aws-access",
  AWS_SECRET_ACCESS_KEY: "aws-secret",
  AWS_SESSION_TOKEN: "aws-session",
  AWS_CONTAINER_CREDENTIALS_FULL_URI: "http://169.254.170.23/v1/credentials",
  AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE: "/run/aws/pod-identity-token",
};
for (const [name, value] of Object.entries(expected)) {
  if (apiEnvironment[name] !== value) {
    throw new Error(`${apiServiceName} ${name}=${apiEnvironment[name]}, want ${value}`);
  }
}

for (const serviceName of ["migration", "sync", "enrich", "trace", "metadata", "maintenance"]) {
  const environment = services[serviceName]?.environment;
  if (!environment) {
    continue;
  }
  for (const name of Object.keys(expected)) {
    if (Object.hasOwn(environment, name)) {
      throw new Error(`${serviceName} unexpectedly receives ${name}`);
    }
  }
}

const storage = services["object-storage"];
if (storage?.image !== "rustfs/rustfs:1.0.0" ||
    JSON.stringify(storage.command) !== JSON.stringify(["/data"])) {
  throw new Error("object-storage must run the pinned RustFS image with /data");
}
if (!storage.environment?.RUSTFS_ACCESS_KEY || !storage.environment?.RUSTFS_SECRET_KEY ||
    Object.keys(storage.environment).some((key) => key.startsWith("MINIO_"))) {
  throw new Error("object-storage requires RustFS credentials without MinIO aliases");
}
if (storage.user || storage.ports?.length ||
    storage.environment.RUSTFS_ADDRESS !== ":9000" ||
    storage.environment.RUSTFS_CONSOLE_ADDRESS !== ":9001" ||
    String(storage.environment.RUSTFS_CONSOLE_ENABLE) !== "true") {
  throw new Error("RustFS must retain its default user and internal listeners");
}
if (!storage.volumes?.some((volume) => volume.type === "volume" &&
    volume.source === "rustfs-data" && volume.target === "/data") ||
    storage.volumes.some((volume) => volume.source === "object-data")) {
  throw new Error("RustFS must use its own fresh data volume");
}
if (JSON.stringify(storage.healthcheck?.test) !==
    JSON.stringify(["CMD", "curl", "-fsS", "http://127.0.0.1:9000/health/ready"])) {
  throw new Error("RustFS storage readiness probe is missing");
}
for (const [name, service] of Object.entries(services)) {
  if (name !== "object-storage" && (service.depends_on?.["object-storage"] ||
      Object.keys(service.environment ?? {}).some((key) => key.startsWith("RUSTFS_")))) {
    throw new Error(`${name} must not depend on RustFS or receive its root credentials`);
  }
}
