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
