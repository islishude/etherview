import { useTranslation } from "react-i18next";
import type { AbiParameter, Address } from "viem";
import { SelectControl } from "@/components/SelectControl";
import { ABI_LIMITS, createAbiArrayItem, type AbiInputNode } from "./abi";

export function AbiInputEditor({
  parameter,
  node,
  path,
  formID,
  disabled,
  onChange,
  onLimitError,
  selfAddress,
  walletShortcut = true,
}: {
  parameter: AbiParameter;
  node: AbiInputNode;
  path: number[];
  formID: string;
  disabled: boolean;
  onChange: (node: AbiInputNode) => void;
  onLimitError: () => void;
  selfAddress?: Address;
  walletShortcut?: boolean;
}) {
  const { t } = useTranslation();
  const label = parameter.name || `#${path.at(-1) ?? 0}`;
  const inputID = `${formID}-${path.join("-")}`;
  if (node.kind === "scalar") {
    return (
      <label className="abi-scalar-input" htmlFor={inputID}>
        <span className="abi-input-heading">
          <span>{label}</span>
          <small>{parameter.type}</small>
        </span>
        {parameter.type === "bool" ? (
          <SelectControl
            disabled={disabled}
            id={inputID}
            onChange={(event) => onChange({ ...node, value: event.target.value })}
            value={node.value}
          >
            <option value="">—</option>
            <option value="true">true</option>
            <option value="false">false</option>
          </SelectControl>
        ) : parameter.type === "string" || parameter.type === "bytes" ? (
          <textarea
            disabled={disabled}
            id={inputID}
            maxLength={
              parameter.type === "string" ? ABI_LIMITS.stringBytes : ABI_LIMITS.bytesLength * 2 + 2
            }
            onChange={(event) => onChange({ ...node, value: event.target.value })}
            rows={2}
            spellCheck={false}
            value={node.value}
          />
        ) : parameter.type === "address" ? (
          <div className="abi-address-control">
            <input
              disabled={disabled}
              id={inputID}
              maxLength={42}
              onChange={(event) => onChange({ ...node, value: event.target.value })}
              spellCheck={false}
              value={node.value}
            />
            {walletShortcut && (
              <button
                aria-label={t("contracts.functions.useSelf")}
                className="abi-self-button"
                disabled={disabled || !selfAddress}
                onClick={() => {
                  if (selfAddress) onChange({ ...node, value: selfAddress });
                }}
                title={t("contracts.functions.useSelf")}
                type="button"
              >
                {t("contracts.functions.self")}
              </button>
            )}
          </div>
        ) : (
          <input
            disabled={disabled}
            id={inputID}
            maxLength={parameter.type === "address" ? 42 : 160}
            onChange={(event) => onChange({ ...node, value: event.target.value })}
            spellCheck={false}
            value={node.value}
          />
        )}
      </label>
    );
  }
  if (node.kind === "tuple") {
    const components = "components" in parameter ? parameter.components : [];
    return (
      <fieldset className="abi-composite-input">
        <legend>
          {label} <small>{parameter.type}</small>
        </legend>
        {node.fields.map((field, index) => (
          <AbiInputEditor
            disabled={disabled}
            formID={formID}
            key={index}
            node={field}
            onChange={(next) =>
              onChange({
                ...node,
                fields: replaceRootNode(node.fields, index, next),
              })
            }
            onLimitError={onLimitError}
            parameter={components[index]!}
            path={[...path, index]}
            selfAddress={selfAddress}
            walletShortcut={walletShortcut}
          />
        ))}
      </fieldset>
    );
  }

  const element = arrayElementParameter(parameter);
  return (
    <fieldset className="abi-composite-input abi-array-input">
      <legend>
        {label} <small>{parameter.type}</small>
      </legend>
      {node.items.map((item, index) => (
        <div className="abi-array-item" key={index}>
          <AbiInputEditor
            disabled={disabled}
            formID={formID}
            node={item}
            onChange={(next) =>
              onChange({
                ...node,
                items: replaceRootNode(node.items, index, next),
              })
            }
            onLimitError={onLimitError}
            parameter={element}
            path={[...path, index]}
            selfAddress={selfAddress}
            walletShortcut={walletShortcut}
          />
          {node.fixedLength === null ? (
            <button
              className="button tertiary"
              disabled={disabled}
              onClick={() =>
                onChange({
                  ...node,
                  items: node.items.filter((_, itemIndex) => itemIndex !== index),
                })
              }
              type="button"
            >
              {t("contracts.functions.removeArrayItem")}
            </button>
          ) : null}
        </div>
      ))}
      {node.fixedLength === null && node.items.length < ABI_LIMITS.dynamicArrayLength ? (
        <button
          className="button secondary"
          disabled={disabled}
          onClick={() => {
            try {
              onChange({
                ...node,
                items: [...node.items, createAbiArrayItem(parameter)],
              });
            } catch {
              onLimitError();
            }
          }}
          type="button"
        >
          {t("contracts.functions.addArrayItem")}
        </button>
      ) : null}
    </fieldset>
  );
}

export function replaceRootNode(
  nodes: readonly AbiInputNode[],
  index: number,
  next: AbiInputNode,
): readonly AbiInputNode[] {
  return nodes.map((node, nodeIndex) => (nodeIndex === index ? next : node));
}

function arrayElementParameter(parameter: AbiParameter): AbiParameter {
  const type = parameter.type.replace(/\[[0-9]*\]$/u, "");
  return { ...parameter, type } as AbiParameter;
}
