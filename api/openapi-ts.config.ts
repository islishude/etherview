import { defineConfig } from "@hey-api/openapi-ts";

export default defineConfig({
  input: "./openapi.yaml",
  output: "../web/src/api/heyapi",
  plugins: [
    "@hey-api/client-fetch",
    "@hey-api/typescript",
    {
      name: "@hey-api/sdk",
      // Every operation must receive the guarded project client explicitly.
      client: false,
      operations: { strategy: "flat" },
      paramsStructure: "grouped",
    },
  ],
});
