// One reviewed mapping for genesis generation, manifest generation and CLI checks.
export const defiDeployments = [
  { protocol: "uniswap-v2", contracts: { factory: "PreviewDeFiV2Factory" } },
  {
    protocol: "uniswap-v3",
    contracts: { factory: "PreviewDeFiV3Factory", position_manager: "PreviewDeFiV3Positions" },
  },
  {
    protocol: "uniswap-v4",
    contracts: {
      pool_manager: "PreviewDeFiV4Manager",
      position_manager: "PreviewDeFiV4Positions",
      state_view: "PreviewDeFiV4View",
    },
  },
  {
    protocol: "aave-v3",
    contracts: { pool: "PreviewDeFiAavePool", data_provider: "PreviewDeFiAaveProvider" },
    receipts: ["PreviewDeFiSupply", "PreviewDeFiDebt"],
  },
  {
    protocol: "curve",
    contracts: { metaregistry: "PreviewDeFiCurveRegistry" },
    pools: ["PreviewDeFiCurveLP"],
  },
  {
    protocol: "lido",
    contracts: {
      steth: "PreviewDeFiStETH",
      wsteth: "PreviewDeFiWstETH",
      withdrawal_queue: "PreviewDeFiWithdrawals",
    },
  },
];
export const defiArtifacts = {
  PreviewDeFiToken0: "PreviewDeFiToken",
  PreviewDeFiToken1: "PreviewDeFiToken",
  PreviewDeFiCurveLP: "PreviewDeFiToken",
  PreviewDeFiStETH: "PreviewDeFiToken",
  PreviewDeFiSupply: "PreviewDeFiReceipt",
  PreviewDeFiDebt: "PreviewDeFiReceipt",
  ...Object.fromEntries(
    [
      "V2Factory",
      "V2Pair",
      "V3Pool",
      "V3Factory",
      "V3Positions",
      "V4Manager",
      "V4Positions",
      "V4View",
      "AavePool",
      "AaveProvider",
      "CurveRegistry",
      "WstETH",
      "Withdrawals",
    ].map((n) => ["PreviewDeFi" + n, "PreviewDeFi" + n]),
  ),
};
export async function deployDeFi(deploy, write) {
  const a = await deploy("PreviewDeFiToken0", ["Preview Asset A"]);
  const b = await deploy("PreviewDeFiToken1", ["Preview Asset B"]);
  const f = await deploy("PreviewDeFiV2Factory");
  const pair = await deploy("PreviewDeFiV2Pair", [f, a, b]);
  await write("PreviewDeFiV2Factory", "initialize", [pair]);
  const pool = await deploy("PreviewDeFiV3Pool");
  const v3 = await deploy("PreviewDeFiV3Factory", [pool, a, b]);
  await deploy("PreviewDeFiV3Positions", [v3, a, b]);
  const manager = await deploy("PreviewDeFiV4Manager");
  await deploy("PreviewDeFiV4Positions", [manager, a, b]);
  await deploy("PreviewDeFiV4View", [manager]);
  const lending = await deploy("PreviewDeFiAavePool", [a]);
  const supply = await deploy("PreviewDeFiSupply", [lending, a, "Preview aToken"]);
  const debt = await deploy("PreviewDeFiDebt", [lending, a, "Preview debt"]);
  await deploy("PreviewDeFiAaveProvider", [a, supply, debt]);
  const lp = await deploy("PreviewDeFiCurveLP", ["Preview Curve LP"]);
  await deploy("PreviewDeFiCurveRegistry", [lp, a, b]);
  await deploy("PreviewDeFiStETH", ["Preview stETH"]);
  await deploy("PreviewDeFiWstETH");
  await deploy("PreviewDeFiWithdrawals");
}
