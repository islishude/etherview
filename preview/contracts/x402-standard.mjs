// Standard upstream runtime with constructor immutables initialized for genesis.
import fs from "node:fs";

import { encodeAbiParameters, keccak256, stringToHex, toHex } from "viem";
const artifacts = JSON.parse(
  fs.readFileSync(new URL("upstream/x402-standard.json", import.meta.url)),
);
export function standardPaymentContracts(chainId) {
  const permit2 = "0x000000000022D473030F116dDEE9F6B43aC78BA3";
  const proxy = "0x402085c248EeA27D92E8b30b2C58ed07f9E20001";
  const domain = keccak256(
    encodeAbiParameters(
      [{ type: "bytes32" }, { type: "bytes32" }, { type: "uint256" }, { type: "address" }],
      [
        keccak256(
          stringToHex("EIP712Domain(string name,uint256 chainId,address verifyingContract)"),
        ),
        keccak256(stringToHex("Permit2")),
        BigInt(chainId),
        permit2,
      ],
    ),
  );
  return Object.entries(artifacts).map(([name, artifact]) => {
    const code = Buffer.from(artifact.bytecode.slice(2), "hex");
    const values = {
      cachedDomainSeparator: domain,
      cachedChainId: toHex(BigInt(chainId), { size: 32 }),
      permit2: toHex(BigInt(permit2), { size: 32 }),
    };
    for (const [key, refs] of Object.entries(artifact.immutables)) {
      for (const ref of refs) {
        if (ref.length !== 32) throw Error("unexpected immutable width");
        Buffer.from(values[key].slice(2), "hex").copy(code, ref.start);
      }
    }
    return {
      name,
      address: name === "Permit2" ? permit2 : proxy,
      abi: artifact.abi,
      code: `0x${code.toString("hex")}`,
    };
  });
}
if (process.argv.includes("--json"))
  console.log(JSON.stringify(standardPaymentContracts(Number(process.argv.at(-1)))));

if (process.argv.includes("--write-fixture")) {
  for (const contract of standardPaymentContracts(31337))
    fs.writeFileSync(
      new URL(`../../e2e/x402local/contracts/${contract.name}.hex`, import.meta.url),
      contract.code + "\n",
    );
}
