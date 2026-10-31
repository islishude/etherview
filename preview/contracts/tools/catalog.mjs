const scenario = (title, requires, actions, checks) => ({ title, requires, actions, checks });
export const catalog = {
  transactions: scenario(
    "普通交易 / Native transactions",
    [],
    ["activity"],
    ["交易类型、费用、内部转账、日志、Trace、状态变化与 revert 原因"],
  ),
  blob: scenario(
    "Blob 单个与多个",
    ["blobs", "consensus"],
    [],
    [
      "区块/交易关联、版本化哈希、KZG commitment、归档下载 SHA-256",
      "共识 slot 和真实 execution requests；空集合不代表非空请求已验收",
    ],
  ),
  aa: scenario(
    "ERC-4337 / Rundler",
    ["user_operations"],
    ["execute", "revert", "bad-signature", "insufficient-prefund"],
    [
      "UserOperation 与打包交易分别检查；账户、factory、事件、费用和执行结果",
      "拒绝操作没有链上记录，不等同于已上链执行失败",
    ],
  ),
  tokens: scenario(
    "ERC-20 / ERC-721 / ERC-1155",
    ["nft_metadata"],
    ["transfer", "approve", "burn", "metadata-update", "metadata-invalid", "activity"],
    [
      "余额、持有人、批量转账、大整数 Token ID 和分页",
      "NFT 元数据及更新；本地 IPFS 图片仍需人工确认访问",
    ],
  ),
  ens: scenario(
    "本地 .test ENS",
    ["ens"],
    ["renew", "transfer", "reclaim", "set-address", "set-primary", "mismatch"],
    [
      "搜索、名称详情和历史；Registrant 与 Controller 分离",
      "只有匹配的 reverse/forward 才显示主名称",
    ],
  ),
  contracts: scenario(
    "源码验证与 artifact 复用",
    ["verification"],
    ["verify"],
    [
      "独立未验证、exact-address 验证和同代码 artifact 样例",
      "源码、ABI、构造参数、验证上传文件与读写交互",
    ],
  ),
  "proxy.erc1967": scenario(
    "ERC-1967",
    ["verification"],
    ["write", "upgrade"],
    ["实现、代理存储、源码、ABI、升级历史"],
  ),
  "proxy.transparent": scenario(
    "Transparent / ProxyAdmin",
    ["verification"],
    ["write", "upgrade"],
    ["实现与 immutable ProxyAdmin；管理目标和升级历史"],
  ),
  "proxy.uups": scenario(
    "UUPS",
    ["verification"],
    ["write", "bad-upgrade", "upgrade"],
    ["实现与 UUPS 升级权限；错误 UUID 失败不改变实现"],
  ),
  "proxy.beacon": scenario(
    "Beacon / 两个代理",
    ["verification"],
    ["write", "upgrade"],
    ["两个代理共享 Beacon 实现、各自保留存储"],
  ),
  "proxy.erc1167": scenario(
    "ERC-1167 clone",
    ["verification"],
    ["write"],
    ["工厂创建、实现关系；没有升级管理入口"],
  ),
  "proxy.immutable": scenario(
    "OpenZeppelin immutable-args clone",
    ["verification"],
    ["write"],
    ["克隆尾部 immutable args、实现与交互"],
  ),
  "proxy.cwia": scenario(
    "Solady legacy CWIA",
    ["verification"],
    ["write", "unauthorized"],
    ["原始和解码 immutable args、大整数、动态 bytes、写入权限"],
  ),
  "proxy.safe": scenario(
    "Safe 1.4.1",
    [],
    [],
    [
      "官方 shell 字节码识别；本地 singleton 不在官方链地址清单，official_singleton=false；不授予普通代理管理权限",
    ],
  ),
  "proxy.diamond": scenario(
    "ERC-2535 Diamond",
    ["verification"],
    ["write", "add", "replace", "remove"],
    ["facet 与 selector 路由、DiamondCut 历史、在 Diamond 地址执行"],
  ),
  eip7702: scenario(
    "EIP-7702",
    ["historical_state", "trace", "verification"],
    ["execute", "redelegate", "skip", "revert", "clear"],
    [
      "授权 applied/skipped、委托历史、实际执行代码地址、ABI/日志/Trace",
      "执行 revert 后授权仍存在；清除后 EOA 状态和历史保留",
    ],
  ),
  wallet: scenario(
    "浏览器钱包与个人功能",
    ["user_auth", "api_billing", "x402_topups"],
    ["activity"],
    [
      "添加 Preview 网络、SIWE 登录、API Key 创建/撤销",
      "Billing 分别检查 EIP-3009/Permit2；不确定支付只复查原支付",
      "关注地址、授权通知后执行 activity，再检查通知和 CSV",
      "推送服务、管理员权限、外部 Sourcify/源码发现需额外条件，默认未验收",
    ],
  ),
};
export const lifecycle = {
  transactions: ["activity"],
  aa: ["execute", "revert", "bad-signature", "insufficient-prefund"],
  tokens: ["approve", "transfer", "burn", "metadata-update", "metadata-invalid"],
  ens: ["renew", "transfer", "reclaim", "set-address", "set-primary", "mismatch"],
  "proxy.erc1967": ["write", "upgrade"],
  "proxy.transparent": ["write", "upgrade"],
  "proxy.uups": ["write", "bad-upgrade", "upgrade"],
  "proxy.beacon": ["write", "upgrade"],
  "proxy.erc1167": ["write"],
  "proxy.immutable": ["write"],
  "proxy.cwia": ["write", "unauthorized"],
  "proxy.diamond": ["write", "add", "replace", "remove"],
  eip7702: ["execute", "redelegate", "skip", "revert", "clear"],
};

export function actionProblem(s, name) {
  const completed = s.actions.filter((a) => a.status === "complete");
  if (s.name === "ens" && name === "reclaim" && !completed.some((a) => a.name === "transfer"))
    return "ENS reclaim requires the transfer action first";
  if (s.name === "proxy.diamond" && name === "remove") {
    const changes = completed.filter((a) => a.name === "add" || a.name === "remove");
    if (changes.at(-1)?.name !== "add") return "Diamond remove requires the add action first";
  }
}
