import i18n from "../index";

export const billingPriceResources = {
  en: {
    billing: {
      result: {
        success: "Top-up successful",
        failure: "Top-up unsuccessful",
        continue: "Continue topping up",
        history: "View top-up history",
      },
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
        query: "Check balance",
        queryNetwork: "Check balance to temporarily switch to the payment network, then return.",
        loading: "Checking balance…",
        switchError: {
          rejected:
            "Network switch cancelled. Your wallet is not ready to sign payment on {{network}}. Confirm the switch in your wallet and retry.",
          mismatch:
            "Your wallet did not switch to {{network}}. Payment signing is blocked. Retry the network switch.",
          invalid:
            "Your wallet returned an invalid network-switch response for {{network}}. Payment signing is blocked. Retry the network switch.",
          failed:
            "Unable to switch to {{network}}. Your wallet is not ready to sign payment on this network. Confirm the switch in your wallet and retry.",
        },
        balanceUnavailable: "Unavailable",
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
        creditUnit: "Credits",
        expectedCredit: "Expected credit: {{amount}} credits",
        exchangeRate: "1 token = {{rate}} credits",
        prices: "Query prices",
        weight: "Complexity weight",
        pricePolicy:
          "Each successful query costs its fixed complexity weight × base price. Rate limits are separate.",
      },
    },
  },
  zh: {
    billing: {
      result: {
        success: "充值成功",
        failure: "充值未完成",
        continue: "继续充值",
        history: "查看充值历史",
      },
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
        query: "查询余额",
        queryNetwork: "点击查询余额，临时切换到支付网络，查询后返回原网络。",
        loading: "正在检查余额…",
        switchError: {
          rejected:
            "已取消网络切换，钱包尚未准备好在 {{network}} 签署付款。请在钱包中确认切网后重试。",
          mismatch: "钱包未实际切换到 {{network}}，暂时无法签署付款。请重试切换网络。",
          invalid: "钱包切换到 {{network}} 时返回异常，暂时无法签署付款。请重试切换网络。",
          failed:
            "未能切换到 {{network}}，钱包尚未准备好在该网络签署付款。请在钱包中确认切网后重试。",
        },
        balanceUnavailable: "不可用",
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
        creditUnit: "额度",
        expectedCredit: "预计到账：{{amount}} 额度",
        exchangeRate: "1 代币 = {{rate}} 额度",
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
