import { getAddress, hexToBytes, type Hex } from "viem";

export type TopicDisplayMode = "hex" | "address" | "text" | "number";

export type LogArgumentInput = Readonly<{
  name: string;
  type: string;
  indexed: boolean;
  value: unknown;
}>;

export type LogArgumentRow = Readonly<{
  path: string;
  type: string;
  indexed?: boolean;
  value: unknown;
  composite: boolean;
  depth: number;
}>;

const TOPIC_PATTERN = /^0x[0-9a-f]{64}$/iu;
const MAX_LOG_ARGUMENT_ROWS = 512;
const MAX_LOG_ARGUMENT_ITEMS = 64;

export function formatTopicValue(topic: string, mode: TopicDisplayMode): string | undefined {
  if (mode === "hex") return topic;
  if (!TOPIC_PATTERN.test(topic)) return undefined;

  if (mode === "address") {
    try {
      return getAddress(`0x${topic.slice(-40)}`);
    } catch {
      return undefined;
    }
  }

  if (mode === "number") {
    try {
      return BigInt(topic).toString(10);
    } catch {
      return undefined;
    }
  }

  try {
    const text = new TextDecoder("utf-8", { fatal: true })
      .decode(hexToBytes(topic as Hex))
      .replace(/\u0000+$/u, "");
    return text.length > 0 ? text : undefined;
  } catch {
    return undefined;
  }
}

export function isAnonymousDecodedLog(
  status: string,
  topicCount: number,
  arguments_: readonly Pick<LogArgumentInput, "indexed">[],
): boolean {
  if (status !== "decoded") return false;
  const indexedCount = arguments_.reduce((count, argument) => count + (argument.indexed ? 1 : 0), 0);
  return topicCount === indexedCount;
}

export function flattenLogArgument(
  argument: LogArgumentInput,
  index: number,
  maximumDepth = 16,
  maximumRows = MAX_LOG_ARGUMENT_ROWS,
): readonly LogArgumentRow[] {
  const rootPath = argument.name || `[${index}]`;
  const rows: LogArgumentRow[] = [];
  appendLogArgumentRows(rows, rootPath, argument.type, argument.value, argument.indexed, 0, maximumDepth, maximumRows);
  return rows;
}

function appendLogArgumentRows(
  rows: LogArgumentRow[],
  path: string,
  type: string,
  value: unknown,
  indexed: boolean | undefined,
  depth: number,
  maximumDepth: number,
  maximumRows: number,
): void {
  if (rows.length >= maximumRows) return;
  const composite = Array.isArray(value) || isRecord(value);
  rows.push({ path, type, indexed, value, composite, depth });
  if (!composite || depth >= maximumDepth) return;

  if (Array.isArray(value)) {
    const childType = arrayElementType(type);
    value.slice(0, MAX_LOG_ARGUMENT_ITEMS).forEach((child, index) => {
      appendLogArgumentRows(rows, `${path}[${index}]`, childType, child, undefined, depth + 1, maximumDepth, maximumRows);
    });
    return;
  }

  for (const [key, child] of Object.entries(value).slice(0, MAX_LOG_ARGUMENT_ITEMS)) {
    const childPath = isSimpleIdentifier(key) ? `${path}.${key}` : `${path}[${JSON.stringify(key)}]`;
    appendLogArgumentRows(rows, childPath, "", child, undefined, depth + 1, maximumDepth, maximumRows);
  }
}

function arrayElementType(type: string): string {
  return type.replace(/\[[0-9]*\]$/u, "") || "";
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isSimpleIdentifier(value: string): boolean {
  return /^[A-Za-z_$][A-Za-z0-9_$]*$/u.test(value);
}
