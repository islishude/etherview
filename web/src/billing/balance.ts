import { decodeFunctionResult, encodeFunctionData, getAddress, type Address, type Hex } from "viem";

const balanceABI = [
  {
    type: "function",
    name: "balanceOf",
    stateMutability: "view",
    inputs: [{ name: "owner", type: "address" }],
    outputs: [{ name: "", type: "uint256" }],
  },
] as const;

export class TopupBalanceError extends Error {
  constructor(readonly reason: "unavailable" | "insufficient") {
    super("Top-up balance check failed");
    this.name = "TopupBalanceError";
  }
}

export async function readTopupBalance(
  readContract: (call: { to: Address; data: Hex }, chainID: string) => Promise<Hex>,
  account: string,
  asset: string,
  chainID: string,
): Promise<bigint> {
  try {
    const data = await readContract(
      {
        to: getAddress(asset),
        data: encodeFunctionData({
          abi: balanceABI,
          functionName: "balanceOf",
          args: [getAddress(account)],
        }),
      },
      chainID,
    );
    return decodeFunctionResult({ abi: balanceABI, functionName: "balanceOf", data });
  } catch {
    throw new TopupBalanceError("unavailable");
  }
}

export async function checkTopupBalance(
  readContract: (call: { to: Address; data: Hex }, chainID: string) => Promise<Hex>,
  account: string,
  asset: string,
  chainID: string,
  amount: string,
): Promise<bigint> {
  const balance = await readTopupBalance(readContract, account, asset, chainID);
  if (balance < BigInt(amount)) throw new TopupBalanceError("insufficient");
  return balance;
}
