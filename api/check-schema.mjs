import { resolve } from "node:path";

import SwaggerParser from "@apidevtools/swagger-parser";

const source = process.argv[2] ?? resolve(import.meta.dirname, "openapi.yaml");

try {
  // Use the validator's bundled schemas and local references for an offline check.
  await SwaggerParser.validate(source, { resolve: { http: false } });
  console.log(`openapi-check: ${source} is valid`);
} catch (error) {
  console.error(`openapi-check: ${error.message}`);
  process.exitCode = 1;
}
