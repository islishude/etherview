import i18n from "../index";

export const billingPriceResources = {
  en: {
    billing: {
      workspace: {
        tokenAmount: "Token amount",
        prices: "Query prices",
        weight: "Complexity weight",
        pricePolicy:
          "Each successful query costs its fixed complexity weight × base price. Rate limits are separate.",
      },
    },
  },
  zh: {
    billing: {
      workspace: {
        tokenAmount: "代币数量",
        prices: "查询价格",
        weight: "复杂度权重",
        pricePolicy: "每次成功查询按固定复杂度权重 × 基价扣费，请求速率限制独立计算。",
      },
    },
  },
} as const;

for (const [language, resource] of Object.entries(billingPriceResources)) {
  i18n.addResourceBundle(language, "translation", resource, true, true);
}
