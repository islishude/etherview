import type { FormattedAbiOutput, FormattedAbiValue } from "@/contracts/abi";

export type FailureArgumentRow = Readonly<{
  path: string;
  type: string;
  data: string;
}>;

export type FailureArgumentRows = Readonly<{
  rows: readonly FailureArgumentRow[];
  truncated: boolean;
}>;

const MAX_FAILURE_ARGUMENT_ROWS = 512;

export function flattenFailureArguments(
  arguments_: readonly FormattedAbiOutput[],
  maximumRows = MAX_FAILURE_ARGUMENT_ROWS,
): FailureArgumentRows {
  const rows: FailureArgumentRow[] = [];
  let truncated = false;

  const append = (path: string, type: string, value: FormattedAbiValue): void => {
    if (rows.length >= maximumRows) {
      truncated = true;
      return;
    }
    if (value.kind === "scalar") {
      rows.push({ path, type, data: value.text });
      return;
    }
    if (value.kind === "array") {
      if (value.items.length === 0) {
        rows.push({ path, type, data: "[]" });
        return;
      }
      value.items.forEach((item, index) => append(`${path}[${index}]`, item.type, item));
      return;
    }
    if (value.fields.length === 0) {
      rows.push({ path, type, data: "()" });
      return;
    }
    value.fields.forEach((field) => append(`${path}[${field.index}]`, field.type, field.value));
  };

  arguments_.forEach((argument) => {
    append(argument.name || `[${argument.index}]`, argument.type, argument.value);
  });
  return Object.freeze({ rows: Object.freeze(rows), truncated });
}
