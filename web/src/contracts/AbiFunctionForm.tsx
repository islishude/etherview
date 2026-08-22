import {
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type FormEvent,
} from "react";
import { Link } from "@tanstack/react-router";
import { useTranslation } from "react-i18next";
import {
  decodeFunctionResult,
  encodeFunctionData,
  toHex,
  toFunctionSelector,
  type Address,
  type Abi,
  type AbiParameter,
  type Hex,
} from "viem";

import { usePublicConfig } from "@/api/hooks";
import { copyTextToClipboard } from "@/components/CopyButton";
import {
  ABI_LIMITS,
  AbiFormError,
  assertAbiInputTreeWithinLimits,
  createAbiArrayItem,
  createAbiInputTree,
  decodeRevert,
  formatAbiResult,
  parseAbiArguments,
  parseVerifiedABI,
  partitionAbiFunctions,
  type AbiFunctionEntry,
  type AbiInputNode,
  type FormattedAbiOutput,
} from "@/contracts/abi";
import { getContractProxyResponse } from "@/contracts/proxy";
import { getAddressDelegation } from "@/contracts/delegation";
import {
  assertInteractionFunctionAllowed,
  captureInteractionFence,
  InteractionFenceError,
  isInteractionFunctionAllowed,
  refreshInteractionTarget,
  submitFencedTransaction,
  type ContractInteractionTarget,
} from "@/contracts/targets";
import {
  chainsMatch,
  WalletBoundaryError,
  walletErrorTranslationKey,
} from "@/wallet/eip6963";
import { useWallet } from "@/wallet/WalletProvider";

export interface AbiFunctionExplorerProps {
  abi: unknown;
  mode: "read" | "write" | "all";
  targets: readonly ContractInteractionTarget[];
  onBindingChanged?: () => void;
}

interface AbiInputTreeState {
  readonly tree?: readonly AbiInputNode[];
  readonly limitError: boolean;
}

export function AbiFunctionExplorer({
  abi: rawABI,
  mode,
  targets,
  onBindingChanged,
}: AbiFunctionExplorerProps) {
  const { t } = useTranslation();
  const parsed = useMemo(() => {
    try {
      const abi = parseVerifiedABI(rawABI);
      const functions = partitionAbiFunctions(abi);
      return { abi, functions, ambiguousSelectors: ambiguousFunctionSelectors(functions) };
    } catch (error) {
      return { error };
    }
  }, [rawABI]);

  if (parsed.error || !parsed.abi || !parsed.functions) {
    return <p className="form-error" role="alert">{t("contracts.functions.invalidABI")}</p>;
  }

  const sections = mode === "all" ? ["read", "write"] as const : [mode] as const;
  return (
    <div className="abi-function-explorer">
      {parsed.ambiguousSelectors && parsed.ambiguousSelectors.size > 0 ? (
        <p className="chain-warning" role="status">
          {t("contracts.functions.selectorCollision", { count: parsed.ambiguousSelectors.size })}
        </p>
      ) : null}
      {sections.map((section) => {
        const entries = parsed.functions?.[section] ?? [];
        const callable = entries.flatMap((entry) => {
          const target = targetForFunction(targets, entry, parsed.ambiguousSelectors);
          return target ? [{ entry, target }] : [];
        });
        return (
          <section className="abi-function-section" key={section}>
            <h2>
              {section === "read" ? t("contracts.readFunctions") : t("contracts.writeFunctions")}
            </h2>
            {callable.length === 0 ? (
              <p className="quiet">
                {section === "read"
                  ? t("contracts.functions.noReadFunctions")
                  : t("contracts.functions.noWriteFunctions")}
              </p>
            ) : (
              <div className="abi-function-list">
                {callable.map(({ entry, target }, index) => (
                  <AbiFunctionCard
                    abi={parsed.abi as Abi}
                    entry={entry}
                    index={index}
                    key={`${target.kind}:${entry.signature}`}
                    onBindingChanged={onBindingChanged}
                    target={target}
                  />
                ))}
              </div>
            )}
          </section>
        );
      })}
    </div>
  );
}

function AbiFunctionCard({
  abi,
  entry,
  target,
  index,
  onBindingChanged,
}: {
  abi: Abi;
  entry: AbiFunctionEntry;
  target: ContractInteractionTarget;
  index: number;
  onBindingChanged?: () => void;
}) {
  const { t } = useTranslation();
  const wallet = useWallet();
  const publicConfig = usePublicConfig();
  const formID = useId();
  const [inputState, setInputState] = useState<AbiInputTreeState>(() => {
    try {
      return { tree: createAbiInputTree(entry.fn.inputs), limitError: false };
    } catch {
      return { limitError: true };
    }
  });
  const [value, setValue] = useState("");
  const [pending, setPending] = useState(false);
  const [copyingCalldata, setCopyingCalldata] = useState(false);
  const [calldataCopied, setCalldataCopied] = useState(false);
  const [actionHintVisible, setActionHintVisible] = useState(false);
  const calldataCopiedTimeoutRef = useRef<number | undefined>(undefined);
  const actionTooltipRef = useRef<HTMLSpanElement | null>(null);
  const [result, setResult] = useState<
    | { kind: "read"; outputs: readonly FormattedAbiOutput[]; context: string }
    | { kind: "write"; hash: Hex; context: string }
  >();
  const [error, setError] = useState<{
    message: string;
    context: string;
    sticky?: boolean;
  }>();
  const expectedChainID = publicConfig.data?.chain_id;
  const context = interactionContext(target, expectedChainID, wallet.active);
  const visibleResult = result?.context === context ? result : undefined;
  const visibleError = error && (error.sticky || error.context === context) ? error : undefined;
  const ready = Boolean(
    wallet.active &&
    expectedChainID &&
    chainsMatch(wallet.active.chainID, expectedChainID),
  );
  const write = entry.fn.stateMutability !== "view" && entry.fn.stateMutability !== "pure";
  const highRisk = isHighRiskFunction(entry.signature);
  const ownershipRisk = isOwnershipRiskFunction(entry.signature);
  const actionDisabledReason = !wallet.active
    ? t("wallet.errors.notConnected")
    : !expectedChainID
      ? t("wallet.errors.chainUnavailable")
      : !chainsMatch(wallet.active.chainID, expectedChainID)
        ? t("wallet.errors.chainMismatch")
        : undefined;
  const tree = inputState.tree;

  useEffect(() => {
    if (!calldataCopied) return;
    calldataCopiedTimeoutRef.current = window.setTimeout(() => {
      setCalldataCopied(false);
    }, 1200);
    return () => {
      if (calldataCopiedTimeoutRef.current !== undefined) {
        window.clearTimeout(calldataCopiedTimeoutRef.current);
      }
    };
  }, [calldataCopied]);

  useEffect(() => {
    if (!actionHintVisible || !actionDisabledReason) return;
    const dismissOnOutsidePointer = (event: PointerEvent) => {
      const target = event.target;
      if (!(target instanceof Node) || !actionTooltipRef.current?.contains(target)) {
        setActionHintVisible(false);
      }
    };
    document.addEventListener("pointerdown", dismissOnOutsidePointer);
    return () => document.removeEventListener("pointerdown", dismissOnOutsidePointer);
  }, [actionDisabledReason, actionHintVisible]);

  if (!tree) {
    return (
      <details className="abi-function-card" open={index === 0}>
        <FunctionSummary entry={entry} index={index} />
        <p className="form-error" role="alert">
          {t("contracts.functions.inputShapeLimit")}
        </p>
      </details>
    );
  }

  const updateTree = (next: readonly AbiInputNode[]) => {
    try {
      assertAbiInputTreeWithinLimits(next);
      setInputState({ tree: next, limitError: false });
    } catch {
      setInputState((current) => ({ ...current, limitError: true }));
    }
  };

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    if (pending) return;
    setResult(undefined);
    setError(undefined);
    let encoded: EncodedFunctionCall;
    try {
      encoded = encodeFunctionCall(entry, tree, value);
      assertInteractionFunctionAllowed(target, entry.signature, write);
    } catch (cause) {
      setError({
        context,
        message: cause instanceof AbiFormError
          ? t("contracts.functions.invalidInput", { path: cause.path })
          : t("contracts.functions.invalidValue"),
      });
      return;
    }

    const active = wallet.getActiveWallet();
    if (!active || !expectedChainID) {
      setError({ context, message: t("wallet.errors.notConnected") });
      return;
    }

    let fence;
    try {
      fence = captureInteractionFence(target, expectedChainID, active);
    } catch (cause) {
      setError({ context, message: interactionErrorMessage(cause, t) });
      return;
    }

    setPending(true);
    try {
      if (entry.fn.stateMutability === "view" || entry.fn.stateMutability === "pure") {
        const freshTarget = await refreshInteractionTarget({
          fence,
          getCurrentWallet: wallet.getActiveWallet,
          loadFreshProxy: getContractProxyResponse,
          loadFreshDelegation: getAddressDelegation,
          requireExactDelegationSnapshot: write,
        });
        assertInteractionFunctionAllowed(freshTarget, entry.signature, write);
        const output = await wallet.readContract(
          {
            to: freshTarget.transactionTarget,
            data: encoded.calldata,
            ...(encoded.callValue === undefined ? {} : { value: encoded.callValue }),
          },
          expectedChainID,
        );
        const decoded = decodeFunctionResult({
          abi: entry.abi,
          functionName: entry.fn.name,
          data: output,
        });
        const completedContext = interactionContext(
          target,
          expectedChainID,
          wallet.getActiveWallet(),
        );
        if (completedContext !== context) {
          setError({
            context: completedContext,
            message: t("wallet.errors.sessionChanged"),
          });
        } else {
          setResult({
            kind: "read",
            outputs: formatAbiResult(entry.fn, decoded),
            context,
          });
        }
      } else {
        const outcome = await submitFencedTransaction({
          fence,
          getCurrentWallet: wallet.getActiveWallet,
          loadFreshProxy: getContractProxyResponse,
          loadFreshDelegation: getAddressDelegation,
          send: (freshTarget, chainID) => wallet.sendTransaction(
            {
              to: freshTarget.transactionTarget,
              data: encoded.calldata,
              ...(encoded.callValue === undefined ? {} : { value: encoded.callValue }),
            },
            chainID,
          ),
        });
        if (outcome.status === "submitted") {
          setResult({ kind: "write", hash: outcome.transactionHash, context });
        } else if (outcome.status === "unknown") {
          const decoded = decodeWalletRevert(abi, outcome.error);
          setError({
            context,
            message: decoded
              ? t("contracts.functions.unknownWithRevert", { reason: decoded.display })
              : t("wallet.errors.transactionOutcomeUnknown"),
            sticky: true,
          });
        } else {
          if (bindingChanged(outcome.error)) onBindingChanged?.();
          const decoded = decodeWalletRevert(abi, outcome.error);
          setError({
            context,
            message: decoded
              ? t("contracts.functions.reportedRevert", {
                  outcome: interactionErrorMessage(outcome.error, t),
                  reason: decoded.display,
                })
              : interactionErrorMessage(outcome.error, t),
          });
        }
      }
    } catch (cause) {
      if (bindingChanged(cause)) onBindingChanged?.();
      const decoded = cause instanceof WalletBoundaryError
        ? decodeRevert(abi, cause.revertData)
        : undefined;
      setError({
        context,
        message: decoded
          ? t("contracts.functions.reverted", { reason: decoded.display })
          : interactionErrorMessage(cause, t),
      });
    } finally {
      setPending(false);
    }
  };

  const copyCalldata = async () => {
    if (copyingCalldata) return;
    setError(undefined);
    setCopyingCalldata(true);
    try {
      const { calldata } = encodeFunctionCall(entry, tree, value);
      await copyTextToClipboard(calldata);
      setCalldataCopied(true);
    } catch (cause) {
      setCalldataCopied(false);
      setError({
        context,
        message: cause instanceof AbiFormError
          ? t("contracts.functions.invalidInput", { path: cause.path })
          : t("contracts.functions.copyFailed"),
      });
    } finally {
      setCopyingCalldata(false);
    }
  };

  return (
    <details className="abi-function-card" open={index === 0}>
      <FunctionSummary entry={entry} index={index} />
      <form aria-busy={pending} className="abi-function-form" onSubmit={(event) => void submit(event)}>
        {highRisk ? (
          <p className="risk-notice" role="note">
            {t(ownershipRisk
              ? "contracts.functions.ownershipRisk"
              : "contracts.functions.upgradeRisk")}
          </p>
        ) : null}
        {target.kind === "uups_implementation_direct" ? (
          <p className="context-note" role="note">{t("contracts.functions.uupsDirect")}</p>
        ) : null}
        {inputState.limitError ? (
          <p className="form-error" role="alert">{t("contracts.functions.inputShapeLimit")}</p>
        ) : null}
        {tree.map((node, parameterIndex) => (
          <AbiInputEditor
            disabled={pending}
            formID={formID}
            key={parameterIndex}
            node={node}
            onChange={(next) => updateTree(replaceRootNode(tree, parameterIndex, next))}
            onLimitError={() => setInputState((current) => ({ ...current, limitError: true }))}
            parameter={entry.fn.inputs[parameterIndex]!}
            path={[parameterIndex]}
            selfAddress={wallet.active?.account}
          />
        ))}
        {entry.payable ? (
          <label className="abi-scalar-input" htmlFor={`${formID}-value`}>
            <span className="abi-input-heading">
              <span>{t("contracts.functions.nativeValue")}</span>
              <small>uint256 · wei</small>
            </span>
            <input
              disabled={pending}
              id={`${formID}-value`}
              inputMode="numeric"
              maxLength={78}
              onChange={(event) => setValue(event.target.value)}
              pattern="[0-9]*"
              value={value}
            />
          </label>
        ) : null}
        <div className="abi-function-actions">
          <span
            aria-describedby={actionDisabledReason && actionHintVisible ? `${formID}-action-hint` : undefined}
            className={`abi-action-tooltip${actionHintVisible ? " is-open" : ""}`}
            onBlur={(event) => {
              const relatedTarget = event.relatedTarget;
              if (!(relatedTarget instanceof Node) || !event.currentTarget.contains(relatedTarget)) {
                setActionHintVisible(false);
              }
            }}
            onFocus={() => setActionHintVisible(true)}
            onMouseEnter={() => setActionHintVisible(true)}
            onMouseLeave={() => setActionHintVisible(false)}
            ref={actionTooltipRef}
            tabIndex={actionDisabledReason ? 0 : undefined}
          >
            <button className={highRisk ? "button danger" : "button primary"} disabled={!ready || pending} type="submit">
              {pending
                ? t("contracts.functions.pending")
                : entry.fn.stateMutability === "view" || entry.fn.stateMutability === "pure"
                  ? t("actions.read")
                : t("actions.write")}
            </button>
            {actionDisabledReason && actionHintVisible ? (
              <span
                className="abi-action-tooltip-text"
                id={`${formID}-action-hint`}
                role="tooltip"
              >
                {actionDisabledReason}
              </span>
            ) : null}
          </span>
          <button
            aria-label={calldataCopied ? t("common.copied") : t("contracts.functions.copyCalldata")}
            className="button secondary"
            disabled={copyingCalldata}
            onClick={() => void copyCalldata()}
            type="button"
          >
            {calldataCopied ? t("common.copied") : t("contracts.functions.copyCalldata")}
          </button>
        </div>
      </form>
      {visibleError ? <p className="form-error" role="alert">{visibleError.message}</p> : null}
      {visibleResult?.kind === "write" ? (
        <output className="call-result" role="status">
          <span>{t("wallet.transactionHash")}</span>
          <Link
            params={{ hash: visibleResult.hash }}
            search={{ tab: "overview" }}
            to="/tx/$hash"
          >
            <code>{visibleResult.hash}</code>
          </Link>
        </output>
      ) : null}
      {visibleResult?.kind === "read" ? (
        <output className="abi-output" role="status">
          <strong>{t("wallet.result")}</strong>
          {visibleResult.outputs.length === 0 ? (
            <span>{t("contracts.functions.noOutputs")}</span>
          ) : visibleResult.outputs.map((output) => (
            <span key={output.index}>
              <small>{output.name || `#${output.index}`} · {output.type}</small>
              <code>{output.display}</code>
            </span>
          ))}
        </output>
      ) : null}
    </details>
  );
}

function FunctionSummary({ entry, index }: { entry: AbiFunctionEntry; index: number }) {
  return (
    <summary>
      <span className="function-index">{index + 1}</span>
      <code>{entry.signature}</code>
      <span className={`function-mutability ${entry.fn.stateMutability}`}>
        {entry.fn.stateMutability}
      </span>
    </summary>
  );
}

function AbiInputEditor({
  parameter,
  node,
  path,
  formID,
  disabled,
  onChange,
  onLimitError,
  selfAddress,
}: {
  parameter: AbiParameter;
  node: AbiInputNode;
  path: number[];
  formID: string;
  disabled: boolean;
  onChange: (node: AbiInputNode) => void;
  onLimitError: () => void;
  selfAddress?: Address;
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
          <select
            disabled={disabled}
            id={inputID}
            onChange={(event) => onChange({ ...node, value: event.target.value })}
            value={node.value}
          >
            <option value="">—</option>
            <option value="true">true</option>
            <option value="false">false</option>
          </select>
        ) : parameter.type === "string" || parameter.type === "bytes" ? (
          <textarea
            disabled={disabled}
            id={inputID}
            maxLength={parameter.type === "string" ? ABI_LIMITS.stringBytes : ABI_LIMITS.bytesLength * 2 + 2}
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
        <legend>{label} <small>{parameter.type}</small></legend>
        {node.fields.map((field, index) => (
          <AbiInputEditor
            disabled={disabled}
            formID={formID}
            key={index}
            node={field}
            onChange={(next) => onChange({
              ...node,
              fields: replaceRootNode(node.fields, index, next),
            })}
            onLimitError={onLimitError}
            parameter={components[index]!}
            path={[...path, index]}
            selfAddress={selfAddress}
          />
        ))}
      </fieldset>
    );
  }

  const element = arrayElementParameter(parameter);
  return (
    <fieldset className="abi-composite-input abi-array-input">
      <legend>{label} <small>{parameter.type}</small></legend>
      {node.items.map((item, index) => (
        <div className="abi-array-item" key={index}>
          <AbiInputEditor
            disabled={disabled}
            formID={formID}
            node={item}
            onChange={(next) => onChange({
              ...node,
              items: replaceRootNode(node.items, index, next),
            })}
            onLimitError={onLimitError}
            parameter={element}
            path={[...path, index]}
            selfAddress={selfAddress}
          />
          {node.fixedLength === null ? (
            <button
              className="button tertiary"
              disabled={disabled}
              onClick={() => onChange({ ...node, items: node.items.filter((_, itemIndex) => itemIndex !== index) })}
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

function targetForFunction(
  targets: readonly ContractInteractionTarget[],
  entry: AbiFunctionEntry,
  ambiguousSelectors: ReadonlySet<string> = new Set(),
): ContractInteractionTarget | undefined {
  const write = entry.fn.stateMutability !== "view" && entry.fn.stateMutability !== "pure";
  try {
    if (ambiguousSelectors.has(toFunctionSelector(entry.signature).toLowerCase())) {
      return undefined;
    }
  } catch {
    return undefined;
  }
  return targets.find((target) =>
    isInteractionFunctionAllowed(target, entry.signature, write),
  );
}

function ambiguousFunctionSelectors(functions: {
  read: readonly AbiFunctionEntry[];
  write: readonly AbiFunctionEntry[];
}): ReadonlySet<string> {
  const signatures = new Map<string, Set<string>>();
  for (const entry of [...functions.read, ...functions.write]) {
    try {
      const selector = toFunctionSelector(entry.signature).toLowerCase();
      const candidates = signatures.get(selector) ?? new Set<string>();
      candidates.add(entry.signature);
      signatures.set(selector, candidates);
    } catch {
      continue;
    }
  }
  return new Set(
    [...signatures.entries()]
      .filter(([, candidates]) => candidates.size > 1)
      .map(([selector]) => selector),
  );
}

function replaceRootNode(
  nodes: readonly AbiInputNode[],
  index: number,
  next: AbiInputNode,
): readonly AbiInputNode[] {
  return nodes.map((node, nodeIndex) => nodeIndex === index ? next : node);
}

function arrayElementParameter(parameter: AbiParameter): AbiParameter {
  const type = parameter.type.replace(/\[[0-9]*\]$/u, "");
  return { ...parameter, type } as AbiParameter;
}

interface EncodedFunctionCall {
  readonly callValue?: Hex;
  readonly calldata: Hex;
}

function encodeFunctionCall(
  entry: AbiFunctionEntry,
  tree: readonly AbiInputNode[],
  value: string,
): EncodedFunctionCall {
  const args = parseAbiArguments(entry.fn.inputs, tree);
  const callValue = entry.payable ? parseNativeValue(value) : undefined;
  const calldata = encodeFunctionData({
    abi: entry.abi,
    functionName: entry.fn.name,
    args,
  });
  return { callValue, calldata };
}

function parseNativeValue(value: string): Hex | undefined {
  if (value === "") return undefined;
  if (!/^(?:0|[1-9][0-9]{0,77})$/u.test(value)) {
    throw new AbiFormError("INVALID_ABI_VALUE", "$value");
  }
  const parsed = BigInt(value);
  if (parsed >= 1n << 256n) {
    throw new AbiFormError("INVALID_ABI_VALUE", "$value");
  }
  return toHex(parsed);
}

function interactionContext(
  target: ContractInteractionTarget,
  chainID: string | undefined,
  wallet: { uuid: string; account: string; chainID: string; revision: number } | undefined,
): string {
	return JSON.stringify([
		target.kind,
		target.transactionTarget,
		interactionBindingContext(target),
    chainID ?? "",
    wallet?.uuid ?? "",
    wallet?.account ?? "",
    wallet?.chainID ?? "",
    wallet?.revision ?? 0,
  ]);
}

function interactionBindingContext(target: ContractInteractionTarget): readonly string[] | string {
	if (!target.requiresFreshBinding) return "";
	if (target.kind === "delegated_eoa") {
		return [
			target.delegationChainID,
			target.delegationBlockNumber,
			target.delegationBlockHash,
			target.abiAddress,
			target.abiCodeHash ?? "",
		];
	}
	if (target.kind === "diamond_facet") {
		return [
			target.proxyChainID,
			target.proxyAddress,
			target.abiAddress,
			target.abiCodeHash ?? "",
			...target.facetSelectors,
		];
	}
	return [
		target.bindingId ?? "",
		target.proxyCodeHash,
		target.abiAddress,
		target.abiCodeHash ?? "",
		target.abiArtifactResolution ?? "",
		target.beaconAddress ?? "",
		target.beaconCodeHash ?? "",
		target.cwiaSchemaSHA256 ?? "",
	];
}

function isHighRiskFunction(signature: string): boolean {
  return /^(?:upgrade|changeAdmin|transferOwnership\(|renounceOwnership\()/u.test(signature);
}

function isOwnershipRiskFunction(signature: string): boolean {
  return /^(?:transferOwnership\(|renounceOwnership\()/u.test(signature);
}

function decodeWalletRevert(abi: Abi, error: unknown) {
  return error instanceof WalletBoundaryError
    ? decodeRevert(abi, error.revertData)
    : undefined;
}

function bindingChanged(error: unknown): boolean {
  return error instanceof InteractionFenceError && [
    "BINDING_CHANGED",
    "TARGET_CHANGED",
    "FRESH_PROXY_REQUIRED",
  ].includes(error.code);
}

type Translate = ReturnType<typeof useTranslation>["t"];

function interactionErrorMessage(error: unknown, t: Translate): string {
  if (error instanceof WalletBoundaryError) {
    return t(walletErrorTranslationKey(error.code));
  }
  if (error instanceof InteractionFenceError) {
    if (bindingChanged(error)) return t("contracts.functions.bindingChanged");
		if (error.code === "FRESH_PROXY_UNAVAILABLE") {
			return t("contracts.functions.proxyTemporarilyUnavailable");
		}
    if (["CHAIN_CHANGED"].includes(error.code)) return t("wallet.errors.chainMismatch");
    if (["ACCOUNT_CHANGED"].includes(error.code)) return t("wallet.errors.accountChanged");
    if (["PROVIDER_CHANGED", "PROVIDER_REVISION_CHANGED"].includes(error.code)) {
      return t("wallet.errors.sessionChanged");
    }
  }
  if (error instanceof AbiFormError) {
    return t("contracts.functions.invalidInput", { path: error.path });
  }
  return t("wallet.errors.requestFailed");
}
