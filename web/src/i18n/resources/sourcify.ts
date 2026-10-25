import i18n from "../index";

// Loaded by the verification route so these optional forms do not enlarge the shell.
const resources = {
  en: {
    page: {
      verifyDescription:
        "Verify sources in Etherview, import existing Sourcify sources, or publish new sources to Sourcify.",
    },
    verification: {
      signInRequired:
        "Sign in to submit or view verification results. No Etherview API key is needed.",
      signInUnavailable:
        "Browser verification requires user sign-in, which is disabled on this deployment.",
      resultsTitle: "Verification result",
      backToForm: "Back to form",
      retryRead: "Retry loading result",
      resultLinkHelp:
        "Save this page link to check the result later. Returning to the form does not cancel the task. Uploaded files are kept only until you leave or refresh this page.",
      technicalDetails: "Technical details",
      viewContract: "View verified contract",
      compilationFailure:
        "Compilation failed. Check the compiler version, source files and settings, then return to the form to correct them.",
      matchFailure:
        "The compiled bytecode did not match the deployed contract. Check the address, sources and compiler settings.",
      successSummary: "Contract verification succeeded.",
      batchCompleted:
        "Batch processing completed. Individual contracts may have failed verification; use the API to inspect each result.",
      targetChanged:
        "The target contract is no longer on the canonical chain. Check the contract address before submitting again.",
      resourceLimit:
        "The compiler output exceeded the service limit. Reduce the submission size or contact the deployment operator.",
      compilerUnavailable:
        "The required compiler could not run safely. Try again later or contact the deployment operator.",
      upstreamUnavailable: "Sourcify could not complete this request. Try again later.",
      failedSummary:
        "Verification could not complete. Review the error code in technical details before submitting again.",
      cancelledSummary: "This verification task was cancelled.",
      pendingSummary:
        "Your request was accepted. This page updates automatically while verification is in progress.",

      unavailable: "Public verification is unavailable",
      unavailableDetail: "This deployment has disabled new public verification submissions.",
      request: "Verification request",
      securityNotice:
        "Compilation runs asynchronously. Canonical code, block and creation facts are resolved by the server; sign in to submit without an API key.",
      language: "Language",
      inputKind: "Input format",
      compilerVersion: "Compiler version",
      searchVersions: "Search versions…",
      noMatchingVersions: "No matching versions",
      targetFile: "Target file",
      optimizationMode: "Optimization mode",
      compilerDefault: "Compiler default",
      standardJSON: "Standard JSON input",
      multipart: "Multipart sources",
      multipartSources: "Source files",
      geasSources: "Geas source files",
      runtimeEntrypoint: "Runtime entrypoint",
      creationEntrypoint: "Creation entrypoint (optional)",
      contractName: "Contract name (optional)",
      invalidJSON: "Standard JSON input is not valid JSON.",
      duplicateJSONKey: "Standard JSON input contains a duplicate key and was not submitted.",
      inputTooComplex: "Standard JSON input exceeds the supported nesting limit.",
      unsafeJSONNumber:
        "Standard JSON numbers must be safe integers so the submitted compiler input is not changed.",
      invalidJSONObject: "Standard JSON input must be one JSON object.",
      invalidFields: "Provide a valid address and a compiler from the current catalog.",
      submitFailed: "Verification submission failed.",
      submitting: "Submitting…",
      submit: "Submit verification",
      job: "Verification job",
      jobEmpty: "Submit a request to see its asynchronous status here.",
      jobID: "Job ID",
      jobKind: "Job kind",
      result: "Result",
      runtimeMatch: "Runtime match",
      creationMatch: "Creation match",
      creationTransformations: "Creation transformations and values",
      runtimeTransformations: "Runtime transformations and values",
      batchResults: "Batch results",
      errorCode: "Error code",
      updated: "Updated",
      provider: "Verification service",
      localProvider: "Etherview local verification",
      sourcifyProvider: "Sourcify external verification",
      localResultNotice: "This job runs in Etherview. Its result is independent of Sourcify.",
    },
    verificationUpload: {
      allowed: "Allowed file extensions: {{extensions}} (lowercase).",
      selected_one: "{{count}} file selected",
      selected_other: "{{count}} files selected",
      list: "Selected files: {{label}}",
      path: "Source path: {{name}}",
      remove: "Remove",
      removeNamed: "Remove {{name}}",
      invalidExtension: "File {{name}} must use one of these lowercase extensions: {{extensions}}.",
      invalidPath:
        "Invalid source path: {{name}}. Use a relative path without empty, dot or parent segments, backslashes or control characters (maximum 384 UTF-8 bytes).",
      duplicatePath: "Duplicate source path: {{name}}. Give each file a unique relative path.",
      limit:
        "Use at most 1024 files and keep the complete JSON request within 5 MiB; JSON inputs require exactly one file.",
      readFailed: "Could not read the selected files. The previous selection was preserved.",
      required: "Select a file before submitting.",
      requireSourcesMetadata: "Select source files and compiler metadata before submitting.",
      help: "Select files to replace this selection. Edit source paths to match imports; up to 1024 files and a 5 MiB JSON request. Vyper JSON interfaces belong in Standard JSON.",
    },
    sourcify: {
      localChoice: "Upload your sources and verify them against this chain in Etherview.",
      sourcifyChoice: "Sourcify source library",
      sourcifyChoiceHelp:
        "Import verified sources into Etherview, or publish new sources to Sourcify.",
      workflow: "Sourcify workflow",
      importTab: "Import into Etherview",
      publishTab: "Publish to Sourcify",
      importTitle: "Import verified sources",
      importHelp:
        "Already verified on Sourcify? Choose its source chain and enter the contract address. Etherview retrieves and locally verifies the sources before publishing the source and ABI. No files or Etherscan key are needed.",
      importSteps: "Retrieve sources, verify locally, publish in Etherview",
      localCheck: "Local verification",
      importAction: "Import from Sourcify into Etherview",
      importing: "Retrieving sources…",
      importInvalid: "Provide a valid contract address.",
      importFailed: "Could not import sources from Sourcify.",
      notPublished: "Not on Sourcify yet?",
      afterPublish:
        "Retrieve these sources and run Etherview's local verification. The local source and ABI are published only after compilation and on-chain matching succeed.",

      method: "Sourcify submission method",
      sources: "Submit source files",
      etherscan: "Import from Etherscan",
      etherscanAPIKey: "Etherscan API Key (optional)",
      etherscanKeyNotice:
        "Used only for this import and sent to the configured Sourcify service. Encrypted in the server's job storage; never included in job responses. Cleared from this form after submission finishes.",
      invalidEtherscanKey:
        "Use an Etherscan API Key of at most 256 printable ASCII characters without spaces, or leave it empty.",
      chainID: "Chain ID",
      publicationNotice:
        "Submitting sends the source files and metadata to the external Sourcify service for verification and public publication. Etherscan import asks Sourcify to retrieve and publish the contract sources from Etherscan.",
      localBoundary:
        "Sourcify results describe external verification only. They do not mark this contract as locally verified in Etherview or publish a local ABI.",
      consent: "I agree to publish these contract sources through Sourcify.",
      sourceChainID: "Source Chain ID",
      sourceChainHelp:
        "Sourcify lookup chain. The same address is verified locally on Etherview chain {{chain}}; runtime code must match.",
      invalidChainID:
        "Enter a positive decimal Chain ID up to 18446744073709551615, without leading zeros.",
      selectSources: "Source files",
      selectMetadata: "metadata.json",
      chooseFiles: "Choose files",
      replaceFiles: "Replace selection",
      noFileSelected: "No file selected",
      invalidMetadata:
        "Select one valid compiler metadata JSON file containing compiler, settings and output.",
      filesHelp:
        "Include the compiler metadata JSON and all source files. Each upload replaces only its own selection; edit source paths to match those in metadata. Up to 1024 files and a 5 MiB JSON request.",
      etherscanHelp:
        "Sourcify retrieves sources for this chain and address from Etherscan. This does not import a verified contract into Etherview.",
      submitSources: "Publish sources to Sourcify",
      submitEtherscan: "Import from Etherscan and publish to Sourcify",
      reading: "Reading files…",
      invalidFields: "Provide a valid contract address and explicit publication consent.",
      success: "Sourcify verification succeeded (external result)",
      completed: "Completed",
      failure: "Sourcify verification did not succeed",
      externalID: "Sourcify verification ID",
    },
  },
  zh: {
    page: {
      verifyDescription:
        "在 Etherview 验证合约、导入 Sourcify 已有源码，或向 Sourcify 公开发布新源码。",
    },
    verification: {
      signInRequired: "登录后可提交校验或查看结果，无需 Etherview API key。",
      signInUnavailable: "当前部署未启用用户登录，网页校验不可用。",
      resultsTitle: "校验结果",
      backToForm: "返回修改",
      retryRead: "重新加载结果",
      resultLinkHelp:
        "可保存当前页面链接，稍后登录查看结果。返回修改不会取消后台任务；离开或刷新页面后需重新选择上传文件。",
      technicalDetails: "技术详情",
      viewContract: "查看已验证合约",
      compilationFailure: "编译失败，请返回检查编译器版本、源文件和编译设置。",
      matchFailure: "编译后的字节码与链上合约不匹配，请检查地址、源码和编译设置。",
      successSummary: "合约校验成功。",
      batchCompleted: "批量任务已处理完成。部分合约可能未通过校验，请通过 API 查看每项结果。",
      targetChanged: "目标合约已不在规范链上，请确认合约地址后重新提交。",
      resourceLimit: "编译输出超出服务限制，请缩小提交内容或联系部署管理员。",
      compilerUnavailable: "所需编译器无法安全运行，请稍后重试或联系部署管理员。",
      upstreamUnavailable: "Sourcify 未能完成本次请求，请稍后重试。",
      failedSummary: "校验未能完成，请查看技术详情中的错误码后再尝试提交。",
      cancelledSummary: "此校验任务已取消。",
      pendingSummary: "请求已受理，校验过程中此页面会自动更新。",

      targetFile: "目标文件",
      optimizationMode: "优化模式",
      compilerDefault: "编译器默认值",
      unavailable: "公开合约验证不可用",
      unavailableDetail: "当前部署已关闭新的公开验证提交。",
      request: "验证请求",
      securityNotice:
        "编译任务异步执行；规范代码、区块与创建事实由服务端解析，登录后即可提交，无需 API Key。",
      language: "语言",
      inputKind: "输入格式",
      compilerVersion: "编译器版本",
      searchVersions: "搜索版本…",
      noMatchingVersions: "没有匹配的版本",
      standardJSON: "Standard JSON 输入",
      multipart: "Multipart 源码",
      multipartSources: "源码文件",
      geasSources: "Geas 源码文件",
      runtimeEntrypoint: "运行时入口文件",
      creationEntrypoint: "创建入口文件（可选）",
      contractName: "合约名称（可选）",
      invalidJSON: "Standard JSON 输入不是有效 JSON。",
      duplicateJSONKey: "Standard JSON 输入包含重复键，未进行提交。",
      inputTooComplex: "Standard JSON 输入超过支持的嵌套深度。",
      unsafeJSONNumber: "Standard JSON 中的数字必须是安全整数，以免提交的编译器输入被改变。",
      invalidJSONObject: "Standard JSON 输入必须是单个 JSON 对象。",
      invalidFields: "请填写有效地址，并从当前目录选择编译器。",
      submitFailed: "验证请求提交失败。",
      submitting: "正在提交…",
      submit: "提交验证",
      job: "验证任务",
      jobEmpty: "提交请求后将在此显示异步任务状态。",
      jobID: "任务 ID",
      jobKind: "任务类型",
      result: "结果",
      runtimeMatch: "运行时匹配",
      creationMatch: "创建代码匹配",
      creationTransformations: "创建代码 transformations 与 values",
      runtimeTransformations: "运行时代码 transformations 与 values",
      batchResults: "批量结果",
      errorCode: "错误代码",
      updated: "更新时间",
      provider: "验证服务",
      localProvider: "Etherview 本地验证",
      sourcifyProvider: "Sourcify 外部验证",
      localResultNotice: "此任务由 Etherview 执行，结果与 Sourcify 验证相互独立。",
    },
    verificationUpload: {
      allowed: "允许的文件后缀：{{extensions}}（小写）。",
      selected: "已选择 {{count}} 个文件",
      list: "已选文件：{{label}}",
      path: "源码路径：{{name}}",
      remove: "移除",
      removeNamed: "移除 {{name}}",
      invalidExtension: "文件 {{name}} 必须使用以下小写后缀之一：{{extensions}}。",
      invalidPath:
        "源码路径无效：{{name}}。请使用相对路径，不含空路径段、点或父级路径段、反斜杠及控制字符，UTF-8 最多 384 字节。",
      duplicatePath: "源码路径重复：{{name}}。请为每个文件填写唯一的相对路径。",
      limit: "最多 1024 个文件，完整 JSON 请求不超过 5 MiB；JSON 输入须选择一个文件。",
      readFailed: "无法读取所选文件，已保留之前的文件选择。",
      required: "请先选择文件再提交。",
      requireSourcesMetadata: "请先选择源码文件和编译器 metadata 再提交。",
      help: "选择文件将替换当前选择；可编辑源码路径以匹配 import。最多 1024 个文件，完整 JSON 请求不超过 5 MiB。Vyper JSON 接口请通过 Standard JSON 提交。",
    },
    sourcify: {
      localChoice: "提交源码，与当前链上的合约比对，在 Etherview 完成本地验证。",
      sourcifyChoice: "Sourcify 源码库",
      sourcifyChoiceHelp: "将已验证源码导入 Etherview，或向 Sourcify 发布新的合约源码。",
      workflow: "Sourcify 工作流",
      importTab: "导入 Etherview",
      publishTab: "发布至 Sourcify",
      importTitle: "导入已验证源码",
      importHelp:
        "合约已在 Sourcify 验证？选择源码所在链并填写合约地址。Etherview 会获取源码并执行本地验证，通过后发布源码与 ABI。无需上传文件或提供 Etherscan 密钥。",
      importSteps: "获取源码、本地验证、发布至 Etherview",
      localCheck: "本地验证",
      importAction: "从 Sourcify 导入 Etherview",
      importing: "正在获取源码…",
      importInvalid: "请填写有效合约地址。",
      importFailed: "无法从 Sourcify 导入源码。",
      notPublished: "尚未在 Sourcify 验证？",
      afterPublish:
        "获取此合约源码并执行 Etherview 本地验证。只有重新编译并匹配链上代码成功后，才会发布本地源码与 ABI。",

      method: "Sourcify 提交方式",
      sources: "提交源码文件",
      etherscan: "从 Etherscan 导入",
      etherscanAPIKey: "Etherscan API Key（可选）",
      etherscanKeyNotice:
        "仅用于本次导入，会发送给配置的 Sourcify 服务。服务端任务存储中只保存密文，任务查询不会返回此密钥；提交结束后会清空表单中的密钥。",
      invalidEtherscanKey:
        "Etherscan API Key 最多 256 个可打印 ASCII 字符，不能含空格；不提供时请留空。",
      chainID: "链 ID",
      publicationNotice:
        "提交会将源码和 metadata 发送至外部 Sourcify 服务进行验证并公开发布。从 Etherscan 导入会请求 Sourcify 获取并发布 Etherscan 上的合约源码。",
      localBoundary:
        "Sourcify 结果仅代表外部验证，不会将此合约标记为 Etherview 本地已验证，也不会发布本地 ABI。",
      consent: "我同意通过 Sourcify 公开发布这些合约源码。",
      sourceChainID: "源码链 ID",
      sourceChainHelp:
        "用于查询 Sourcify。导入后会在 Etherview 当前链 {{chain}} 上验证同一地址，运行时代码必须匹配。",
      invalidChainID: "请输入不超过 18446744073709551615 的正整数链 ID，不含前导零。",
      selectSources: "源码文件",
      selectMetadata: "metadata.json",
      chooseFiles: "选择文件",
      replaceFiles: "重新选择",
      noFileSelected: "尚未选择文件",
      invalidMetadata:
        "请选择一个有效的编译器 metadata JSON 文件，须包含 compiler、settings 和 output。",
      filesHelp:
        "请包含编译器 metadata JSON 和全部源码。每个上传区仅替换各自的文件；请编辑源码路径，使其与 metadata 中的路径一致。最多 1024 个文件，完整 JSON 请求不超过 5 MiB。",
      etherscanHelp:
        "Sourcify 会从 Etherscan 获取该链和地址的源码。此操作不会向 Etherview 导入已验证合约。",
      submitSources: "向 Sourcify 发布源码",
      submitEtherscan: "从 Etherscan 导入并发布至 Sourcify",
      reading: "正在读取文件…",
      invalidFields: "请填写有效合约地址，并明确同意公开发布源码。",
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
