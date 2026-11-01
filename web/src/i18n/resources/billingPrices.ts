import i18n from "../index";

export const billingPriceResources = {
  en: {
    billing: {
      guide: {
        steps: "Top-up progress",
        connectHint:
          "Connect the wallet for your signed-in account to check your balance and top up.",
        result: "Top-up result",
        enterDescription: "Choose amount and method",
        reviewDescription: "Sign to authorize payment",
        resultDescription: "Wait for credit confirmation",
        success: "Completed",
        error: "Needs attention",
        process: "Current step",
        wait: "Not started",
        walletBalance: "Available in wallet",
        refresh: "Refresh balance",
        loading: "Checking balance…",
        unavailable: "Unable to check wallet balance. Refresh to retry.",
        insufficient: "Wallet token balance is insufficient. Payment was not submitted.",
        shortfall: "Insufficient balance. Missing {{amount}} tokens.",
        enter: "Enter amount",
        review: "Wallet authorization",
        confirm: "Top up & authorize",
        switchNetwork:
          "Confirm to switch to the payment network and check your balance. The wallet returns to the explorer network afterward.",
        authorization: "Your signature authorizes a token payment to fund your API balance.",
        permit2:
          "Permit2 may first require an exact-amount token approval transaction, paid with your wallet’s gas, followed by a payment signature.",
      },

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
      guide: {
        steps: "充值进度",
        connectHint: "请先连接当前登录账户的钱包，以查看余额并充值。",
        result: "充值结果",
        enterDescription: "选择金额与付款方式",
        reviewDescription: "签名授权付款",
        resultDescription: "等待确认到账",
        success: "已完成",
        error: "需要处理",
        process: "当前步骤",
        wait: "未开始",
        walletBalance: "钱包可用",
        refresh: "刷新余额",
        loading: "正在检查余额…",
        unavailable: "无法检查钱包余额，请刷新重试。",
        insufficient: "钱包代币余额不足，付款未提交。",
        shortfall: "余额不足，还差 {{amount}} 个代币。",
        enter: "填写金额",
        review: "钱包授权",
        confirm: "充值并授权",
        switchNetwork: "确认后将切换到支付网络并检查余额，支付结束后切回浏览器所在网络。",
        authorization: "签名将授权实际扣除代币，用于增加站内 API 余额。",
        permit2: "Permit2 可能先要求发送精确金额的代币授权交易，由钱包支付 Gas，然后签署付款授权。",
      },

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
