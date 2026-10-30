import i18n from "../index";

const resources = {
  en: {
    watchlist: {
      exportHint:
        "Up to 31 days and 10,000 rows, as of the indexed snapshot. Raw amounts are exact integers. Import numeric columns as text in spreadsheets to preserve precision. Incomplete data cannot be exported.",
      all: "All",
      signIn: "Sign in",
      watchUnknown: "Watch status unavailable",
      fromUTC: "From (UTC, inclusive)",
      toUTC: "To (UTC, exclusive)",
      download: "Download CSV",
      backToAddress: "Back to address",
      watchSignIn: "Sign in to manage this address watch.",
      exportSignIn: "Sign in to export address activity.",
      signInUnavailable: "Sign-in is unavailable on this deployment.",
      downloaded: "CSV download started.",
      retry: "Retry",
    },
  },
  zh: {
    watchlist: {
      exportHint:
        "最多 31 天、10,000 行，以已索引快照为准。金额为精确原始整数；使用电子表格时请将数值列按文本导入，避免精度损失。数据不完整时无法导出。",
      all: "全部类型",
      signIn: "登录",
      watchUnknown: "关注状态暂不可用",
      fromUTC: "开始时间（UTC，包含）",
      toUTC: "结束时间（UTC，不包含）",
      download: "下载 CSV",
      backToAddress: "返回地址页",
      watchSignIn: "登录后管理此地址的关注状态。",
      exportSignIn: "登录后导出地址活动。",
      signInUnavailable: "当前部署未启用登录。",
      downloaded: "已开始下载 CSV。",
      retry: "重试",
    },
  },
};

for (const [language, resource] of Object.entries(resources)) {
  i18n.addResourceBundle(language, "translation", resource, true, true);
}
