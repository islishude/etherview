import i18n from "../index";

const resources = {
  en: {
    tokenPage: {
      summary: "Token summary",
      contract: "Contract address",
      totalSupply: "Total supply",
      sections: "Token sections",
      transfers: "Transfers",
      details: "Details",
      rawUnits: "(raw units)",
      snapshotSupply: "Reconciled at block",
      observedSupply: "Observed at block",
    },
  },
  zh: {
    tokenPage: {
      summary: "代币概览",
      contract: "合约地址",
      totalSupply: "总供应量",
      sections: "代币栏目",
      transfers: "转账",
      details: "详情",
      rawUnits: "（原始单位）",
      snapshotSupply: "对账区块",
      observedSupply: "观测区块",
    },
  },
};

for (const [language, resource] of Object.entries(resources)) {
  i18n.addResourceBundle(language, "translation", resource, true, true);
}
