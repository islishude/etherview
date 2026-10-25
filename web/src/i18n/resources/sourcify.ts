import i18n from "../index";

// Loaded by the verification route so these optional forms do not enlarge the shell.
const resources = {
  en: {
    page: {
      verifyDescription:
        "Verify contract sources locally in Etherview or publish them through the optional Sourcify service.",
    },
    verification: {
      provider: "Verification service",
      localProvider: "Etherview local verification",
      sourcifyProvider: "Sourcify external verification",
      localResultNotice: "This job runs in Etherview. Its result is independent of Sourcify.",
    },
    sourcify: {
      method: "Sourcify submission method",
      sources: "Submit source files",
      etherscan: "Import from Etherscan",
      chainID: "Chain ID",
      publicationNotice:
        "Submitting sends the source files and metadata to the external Sourcify service for verification and public publication. Etherscan import asks Sourcify to retrieve and publish the contract sources from Etherscan.",
      localBoundary:
        "Sourcify results describe external verification only. They do not mark this contract as locally verified in Etherview or publish a local ABI.",
      consent: "I agree to publish these contract sources through Sourcify.",
      selectFiles: "Select source files and metadata.json",
      files: "Sourcify files (JSON path-to-content map)",
      filesHelp:
        "Include the compiler metadata JSON and all source files. Selecting files replaces the map; edit paths to match those in metadata. Up to 1024 files and a 5 MiB JSON request.",
      etherscanHelp:
        "Sourcify retrieves sources for this chain and address from Etherscan. This does not import a verified contract into Etherview.",
      submitSources: "Publish sources to Sourcify",
      submitEtherscan: "Import from Etherscan and publish to Sourcify",
      reading: "Reading files…",
      readFailed: "Could not read the selected files. Select them again.",
      inputLimit: "Use at most 1024 files and keep the complete JSON request within 5 MiB.",
      duplicateFiles:
        "Selected files have duplicate paths. Use the JSON map to give each source its exact path.",
      invalidFiles:
        "Provide a non-empty JSON path-to-content map with string values, unique keys and at most 1024 files.",
      invalidFields:
        "Provide an API key, a valid contract address and explicit publication consent.",
      success: "Sourcify verification succeeded (external result)",
      completed: "Completed",
      failure: "Sourcify verification did not succeed",
      externalID: "Sourcify verification ID",
    },
  },
  zh: {
    page: {
      verifyDescription: "在 Etherview 本地验证合约源码，或通过可选的 Sourcify 服务公开发布。",
    },
    verification: {
      provider: "验证服务",
      localProvider: "Etherview 本地验证",
      sourcifyProvider: "Sourcify 外部验证",
      localResultNotice: "此任务由 Etherview 执行，结果与 Sourcify 验证相互独立。",
    },
    sourcify: {
      method: "Sourcify 提交方式",
      sources: "提交源码文件",
      etherscan: "从 Etherscan 导入",
      chainID: "链 ID",
      publicationNotice:
        "提交会将源码和 metadata 发送至外部 Sourcify 服务进行验证并公开发布。从 Etherscan 导入会请求 Sourcify 获取并发布 Etherscan 上的合约源码。",
      localBoundary:
        "Sourcify 结果仅代表外部验证，不会将此合约标记为 Etherview 本地已验证，也不会发布本地 ABI。",
      consent: "我同意通过 Sourcify 公开发布这些合约源码。",
      selectFiles: "选择源码文件和 metadata.json",
      files: "Sourcify 文件（JSON 路径到内容映射）",
      filesHelp:
        "请包含编译器 metadata JSON 和全部源码。选择文件会替换映射；请编辑路径，使其与 metadata 中的路径一致。最多 1024 个文件，完整 JSON 请求不超过 5 MiB。",
      etherscanHelp:
        "Sourcify 会从 Etherscan 获取该链和地址的源码。此操作不会向 Etherview 导入已验证合约。",
      submitSources: "向 Sourcify 发布源码",
      submitEtherscan: "从 Etherscan 导入并发布至 Sourcify",
      reading: "正在读取文件…",
      readFailed: "无法读取所选文件，请重新选择。",
      inputLimit: "最多提交 1024 个文件，完整 JSON 请求须在 5 MiB 以内。",
      duplicateFiles: "所选文件路径重复，请在 JSON 映射中填写各源码的准确路径。",
      invalidFiles:
        "请提供非空的 JSON 路径到内容映射，值须为字符串、键不可重复，且最多包含 1024 个文件。",
      invalidFields: "请填写 API Key、有效合约地址，并明确同意公开发布源码。",
      success: "Sourcify 验证成功（外部结果）",
      completed: "已完成",
      failure: "Sourcify 验证未成功",
      externalID: "Sourcify 验证 ID",
    },
  },
};

for (const [language, resource] of Object.entries(resources)) {
  i18n.addResourceBundle(language, "translation", resource, true, true);
}
