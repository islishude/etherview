import type { AbiFunction } from "viem";

import { AbiBudget, AbiFormError } from "./abiTypes";

/** Normalizes only snapshotted data, preserving explicit modern mutability. */
export function parseAbiMutability<const Values extends readonly AbiFunction["stateMutability"][]>(
  record: ReadonlyMap<string, unknown>,
  allowed: Values,
  path: string,
  budget: AbiBudget,
  allowLegacy = true,
): Values[number] {
  for (const key of ["constant", "payable"] as const) {
    if (record.has(key) && typeof record.get(key) !== "boolean") {
      throw new AbiFormError("INVALID_ABI", `${path}.${key}`);
    }
  }
  let stateMutability: unknown;
  if (record.has("stateMutability")) {
    stateMutability = record.get("stateMutability");
    if (typeof stateMutability === "string")
      budget.addBytes(stateMutability, `${path}.stateMutability`);
  } else if (allowLegacy) {
    // Historical flags default to false. Constant cannot distinguish pure from view.
    stateMutability =
      record.get("constant") === true
        ? "view"
        : record.get("payable") === true
          ? "payable"
          : "nonpayable";
  }
  if (typeof stateMutability !== "string" || !allowed.includes(stateMutability as Values[number])) {
    throw new AbiFormError("INVALID_ABI", `${path}.stateMutability`);
  }
  const expected = {
    constant: stateMutability === "view" || stateMutability === "pure",
    payable: stateMutability === "payable",
  };
  for (const key of ["constant", "payable"] as const) {
    if (record.has(key) && record.get(key) !== expected[key]) {
      throw new AbiFormError("INVALID_ABI", `${path}.${key}`);
    }
  }
  return stateMutability as Values[number];
}
