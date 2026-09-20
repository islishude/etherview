import assert from "node:assert/strict";
import fs from "node:fs";

const { services } = JSON.parse(fs.readFileSync(0, "utf8"));
assert.ok(services.ipfs.command.includes("--offline"), "acceptance Kubo must be offline");
assert.equal(services.ipfs.environment.IPFS_GATEWAY_NO_FETCH, "true");
assert.equal(services.metadata.environment.ETHERVIEW_LOG_FORMAT, "json");
assert.equal(services.metadata.environment.ETHERVIEW_METADATA_UNSAFE_ALLOW_PRIVATE_NETWORKS, "true");
for (const [name, service] of Object.entries(services)) {
  for (const port of service.ports ?? []) {
    assert.equal(port.host_ip, "127.0.0.1", `${name} acceptance port must be loopback-only`);
    assert.equal(port.published, "0", `${name} acceptance port must be ephemeral`);
  }
}
assert.equal(services.ipfs.ports[0].target, 5001);
assert.equal(services["ipfs-gateway"].ports[0].target, 8443);
