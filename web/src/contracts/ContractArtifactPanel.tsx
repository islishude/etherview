import { defaultKeymap } from "@codemirror/commands";
import {
  StreamLanguage,
  syntaxHighlighting,
  type StreamParser,
} from "@codemirror/language";
import { highlightSelectionMatches, openSearchPanel, searchKeymap } from "@codemirror/search";
import { EditorState } from "@codemirror/state";
import {
  EditorView,
  highlightSpecialChars,
  keymap,
  lineNumbers,
} from "@codemirror/view";
import { classHighlighter } from "@lezer/highlight";
import { solidity } from "@replit/codemirror-lang-solidity";
import { Link } from "@tanstack/react-router";
import { useEffect, useMemo, useRef, useState } from "react";
import { useTranslation } from "react-i18next";

import { CopyButton } from "@/components/CopyButton";
import { formatTimestamp } from "@/components/format";

import { decodeConstructorArguments } from "./abi";
import type { VerifiedContractArtifact } from "./proxy";

export interface ContractSourceFile {
  name: string;
  content: string;
}

export interface SourceManifest {
  files: ContractSourceFile[];
  invalidEntries: number;
}

export interface ABISummary {
  constructors: number;
  errors: number;
  events: number;
  functions: number;
}

export interface CompilerSettingsSummary {
  optimizerEnabled?: boolean;
  optimizerRuns?: number;
  evmVersion?: string;
  viaIR?: boolean;
  metadata: Array<{ label: string; value: string }>;
  remappings: string[];
}

interface YulState {
  blockComment: boolean;
}

const yulKeywords = new Set([
  "break",
  "case",
  "continue",
  "data",
  "default",
  "for",
  "function",
  "if",
  "leave",
  "let",
  "object",
  "switch",
]);

const yulAtoms = new Set(["false", "true"]);

const yulParser: StreamParser<YulState> = {
  startState: () => ({ blockComment: false }),
  token(stream, state) {
    if (state.blockComment) {
      if (stream.skipTo("*/")) {
        stream.match("*/");
        state.blockComment = false;
      } else {
        stream.skipToEnd();
      }
      return "comment";
    }
    if (stream.eatSpace()) return null;
    if (stream.match("//")) {
      stream.skipToEnd();
      return "comment";
    }
    if (stream.match("/*")) {
      state.blockComment = true;
      return "comment";
    }
    if (stream.match(/^0x[0-9a-fA-F]+/) || stream.match(/^\d+/)) return "number";
    if (stream.match(/^"(?:[^"\\]|\\.)*"/)) return "string";
    if (stream.match(/^[A-Za-z_$][\w$]*/)) {
      const word = stream.current();
      if (yulKeywords.has(word)) return "keyword";
      if (yulAtoms.has(word)) return "atom";
      return "variableName";
    }
    if (stream.match(/^[+\-*/%=&|!<>:]+/)) return "operator";
    stream.next();
    return null;
  },
};

const yulLanguage = StreamLanguage.define(yulParser);

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export function parseArtifactSources(
  sources: Record<string, unknown>,
  mainFile: string,
): SourceManifest {
  const files: ContractSourceFile[] = [];
  let invalidEntries = 0;
  for (const [name, value] of Object.entries(sources)) {
    if (!isRecord(value) || typeof value.content !== "string") {
      invalidEntries += 1;
      continue;
    }
    files.push({ name, content: value.content });
  }
  files.sort((left, right) => {
    if (left.name === mainFile) return -1;
    if (right.name === mainFile) return 1;
    return left.name.localeCompare(right.name);
  });
  return { files, invalidEntries };
}

export function summarizeABI(abi: readonly unknown[]): ABISummary {
  const summary: ABISummary = { constructors: 0, errors: 0, events: 0, functions: 0 };
  for (const item of abi) {
    if (!isRecord(item) || typeof item.type !== "string") continue;
    if (item.type === "constructor") summary.constructors += 1;
    else if (item.type === "error") summary.errors += 1;
    else if (item.type === "event") summary.events += 1;
    else if (item.type === "function") summary.functions += 1;
  }
  return summary;
}

export function summarizeCompilerSettings(
  settings: Record<string, unknown>,
): CompilerSettingsSummary {
  const optimizer = isRecord(settings.optimizer) ? settings.optimizer : undefined;
  const metadata = isRecord(settings.metadata) ? settings.metadata : undefined;
  const optimizerRuns = optimizer?.runs;
  const remappings = Array.isArray(settings.remappings)
    ? settings.remappings.filter((value): value is string => typeof value === "string")
    : [];
  const metadataRows = metadata
    ? Object.entries(metadata)
        .filter((entry): entry is [string, string | number | boolean] =>
          ["string", "number", "boolean"].includes(typeof entry[1]),
        )
        .map(([label, value]) => ({ label, value: String(value) }))
    : [];
  return {
    optimizerEnabled: typeof optimizer?.enabled === "boolean" ? optimizer.enabled : undefined,
    optimizerRuns:
      typeof optimizerRuns === "number" && Number.isSafeInteger(optimizerRuns) && optimizerRuns >= 0
        ? optimizerRuns
        : undefined,
    evmVersion: typeof settings.evmVersion === "string" ? settings.evmVersion : undefined,
    viaIR: typeof settings.viaIR === "boolean" ? settings.viaIR : undefined,
    metadata: metadataRows,
    remappings,
  };
}

export function ContractArtifactPanel({ artifact }: { artifact: VerifiedContractArtifact }) {
  const { i18n, t } = useTranslation();
  const manifest = useMemo(
    () => parseArtifactSources(artifact.sources, artifact.file_name),
    [artifact.file_name, artifact.sources],
  );
  const [selectedName, setSelectedName] = useState(manifest.files[0]?.name ?? "");
  const selected = manifest.files.find((file) => file.name === selectedName) ?? manifest.files[0];
  const settings = useMemo(() => summarizeCompilerSettings(artifact.settings), [artifact.settings]);
  const abi = artifact.abi ?? [];
  const abiSummary = useMemo(() => summarizeABI(abi), [abi]);
  const matchType = artifact.runtime_match?.match_type ?? artifact.creation_match?.match_type;

  useEffect(() => {
    setSelectedName(manifest.files[0]?.name ?? "");
	}, [artifact.source.address, artifact.target.address, artifact.target.code_hash, manifest.files]);

  return (
		<div className="contract-code-view">
			{artifact.resolution === "code_hash" ? (
				<p className="chain-warning" role="status">
					{t("contracts.artifact.similarMatch")}{" "}
					<Link params={{ address: artifact.source.address }} to="/contract/$address">
						<code>{artifact.source.address}</code>
					</Link>
				</p>
			) : null}
      <header className="artifact-hero">
        <div>
          <span className="eyebrow">{t("contracts.artifact.verified")}</span>
          <h2>{artifact.contract_name}</h2>
          <p className="quiet">{t("contracts.readIndependent")}</p>
        </div>
        <div className="artifact-badges" aria-label={t("contracts.artifact.status")}>
          <span className="availability yes">{t("contracts.artifact.verified")}</span>
          {matchType ? (
            <span className={matchType === "full" ? "artifact-match full" : "artifact-match partial"}>
              {t(`contracts.artifact.match.${matchType}`)}
            </span>
          ) : null}
        </div>
      </header>

      <dl className="artifact-summary-grid">
        <SummaryFact label={t("contracts.contractName")} value={artifact.contract_name} />
        <SummaryFact label={t("contracts.fileName")} value={artifact.file_name} mono />
        <SummaryFact label={t("contracts.artifact.language")} value={artifact.language} />
        <SummaryFact label={t("verification.compilerVersion")} value={artifact.compiler_version} mono />
		<SummaryFact label={t("detail.codeHash")} value={artifact.target.code_hash} mono wide />
		<SummaryFact label={t("contracts.artifact.sourceAddress")} value={artifact.source.address} mono wide />
		<SummaryFact
			label={t("contracts.validBlocks")}
			value={`${artifact.source.valid_from_block} – ${artifact.source.valid_to_block ?? "∞"}`}
		/>
		<SummaryFact
			label={t("contracts.artifact.verifiedAt")}
			value={formatTimestamp(artifact.source.created_at, i18n.language)}
        />
        <SummaryFact
          label={t("contracts.artifact.sourceCount")}
          value={String(manifest.files.length)}
        />
      </dl>

      <section className="artifact-section" aria-labelledby="contract-source-title">
        <div className="artifact-section-heading">
          <div>
            <span className="eyebrow">{t("contracts.artifact.sourceEyebrow")}</span>
            <h3 id="contract-source-title">{t("contracts.artifact.sourceTitle")}</h3>
          </div>
          {selected ? <span className="artifact-count">{selected.content.split("\n").length} {t("contracts.artifact.lines")}</span> : null}
        </div>
        {manifest.invalidEntries > 0 ? (
          <p className="chain-warning" role="status">
            {t("contracts.artifact.invalidSources", { count: manifest.invalidEntries })}
          </p>
        ) : null}
        {selected ? (
          <SourceWorkspace
            files={manifest.files}
            language={artifact.language}
            onSelect={setSelectedName}
            selected={selected}
          />
        ) : (
          <p className="form-error" role="alert">{t("contracts.artifact.noReadableSources")}</p>
        )}
        {manifest.invalidEntries > 0 || manifest.files.length === 0 ? (
          <ArtifactDisclosure title={t("contracts.artifact.rawSources")} value={artifact.sources} />
        ) : null}
      </section>

      <CompilerSettings
        libraries={artifact.libraries}
        raw={artifact.settings}
        sourceCount={manifest.files.length}
        summary={settings}
      />

      <section className="artifact-section" aria-labelledby="contract-artifacts-title">
        <div className="artifact-section-heading">
          <div>
            <span className="eyebrow">{t("contracts.artifact.detailsEyebrow")}</span>
            <h3 id="contract-artifacts-title">{t("contracts.artifact.detailsTitle")}</h3>
          </div>
        </div>
        <div className="artifact-disclosure-list">
          <ArtifactDisclosure
            description={t("contracts.artifact.abiSummary", {
              constructors: abiSummary.constructors,
              errors: abiSummary.errors,
              events: abiSummary.events,
              functions: abiSummary.functions,
            })}
            title={t("contracts.abi")}
            value={abi}
          />
          {artifact.constructor_arguments ? (
            <ConstructorArgumentsDisclosure
              abi={artifact.abi ?? []}
              encoded={artifact.constructor_arguments}
            />
          ) : null}
          {artifact.creation_match ? (
            <ArtifactDisclosure
              description={t("contracts.artifact.transformationSummary", {
                count: transformationCount(artifact.creation_match),
              })}
              title={t("verification.creationMatch")}
              value={artifact.creation_match}
            />
          ) : null}
          {artifact.runtime_match ? (
            <ArtifactDisclosure
              description={t("contracts.artifact.transformationSummary", {
                count: transformationCount(artifact.runtime_match),
              })}
              title={t("verification.runtimeMatch")}
              value={artifact.runtime_match}
            />
          ) : null}
          <ArtifactDisclosure title={t("contracts.compilationArtifacts")} value={artifact.compilation_artifacts} />
          <ArtifactDisclosure title={t("contracts.creationArtifacts")} value={artifact.creation_code_artifacts} />
          <ArtifactDisclosure title={t("contracts.runtimeArtifacts")} value={artifact.runtime_code_artifacts} />
        </div>
      </section>
    </div>
  );
}

function transformationCount(
  match: VerifiedContractArtifact["creation_match"],
): number {
  return Array.isArray(match?.transformations) ? match.transformations.length : 0;
}

function SourceWorkspace({
  files,
  language,
  onSelect,
  selected,
}: {
  files: ContractSourceFile[];
  language: VerifiedContractArtifact["language"];
  onSelect: (name: string) => void;
  selected: ContractSourceFile;
}) {
  const { t } = useTranslation();
  const [wrap, setWrap] = useState(false);
  const editorRef = useRef<EditorView | null>(null);
  const hostRef = useRef<HTMLDivElement>(null);
  const workspaceRef = useRef<HTMLDivElement>(null);
  const [fullscreen, setFullscreen] = useState(false);
  const fullscreenAvailable = typeof document !== "undefined" && document.fullscreenEnabled === true;

  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const languageExtension = language === "solidity"
      ? solidity
      : language === "yul"
        ? yulLanguage
        : [];
    const view = new EditorView({
      state: EditorState.create({
        doc: selected.content,
        extensions: [
          lineNumbers(),
          highlightSpecialChars(),
          highlightSelectionMatches(),
          syntaxHighlighting(classHighlighter, { fallback: true }),
          keymap.of([...searchKeymap, ...defaultKeymap]),
          EditorState.readOnly.of(true),
          EditorView.editable.of(false),
          EditorView.contentAttributes.of({
            "aria-label": t("contracts.artifact.readOnlyEditor", { file: selected.name }),
          }),
          ...(wrap ? [EditorView.lineWrapping] : []),
          languageExtension,
        ],
      }),
      parent: host,
    });
    editorRef.current = view;
    return () => {
      editorRef.current = null;
      view.destroy();
    };
  }, [language, selected.content, selected.name, t, wrap]);

  useEffect(() => {
    const update = () => setFullscreen(document.fullscreenElement === workspaceRef.current);
    document.addEventListener("fullscreenchange", update);
    return () => document.removeEventListener("fullscreenchange", update);
  }, []);

  const toggleFullscreen = async () => {
    if (!workspaceRef.current || !fullscreenAvailable) return;
    try {
      if (document.fullscreenElement === workspaceRef.current) await document.exitFullscreen();
      else await workspaceRef.current.requestFullscreen();
    } catch {
      setFullscreen(false);
    }
  };

  return (
    <div className="source-workspace" ref={workspaceRef}>
      <label className="source-file-select-label">
        <span>{t("contracts.artifact.files")}</span>
        <select value={selected.name} onChange={(event) => onSelect(event.target.value)}>
          {files.map((file) => <option key={file.name} value={file.name}>{file.name}</option>)}
        </select>
      </label>
      <aside className="source-file-list" aria-label={t("contracts.artifact.files")}>
        <div className="source-file-list-title">
          <span>{t("contracts.artifact.files")}</span>
          <strong>{files.length}</strong>
        </div>
        <nav>
          {files.map((file) => (
            <button
              aria-current={file.name === selected.name ? "page" : undefined}
              className={file.name === selected.name ? "active" : undefined}
              key={file.name}
              onClick={() => onSelect(file.name)}
              title={file.name}
              type="button"
            >
              <span aria-hidden="true">◇</span>
              <span>{file.name}</span>
            </button>
          ))}
        </nav>
      </aside>
      <div className="source-editor-shell">
        <div className="source-editor-toolbar">
          <strong title={selected.name}>{selected.name}</strong>
          <div>
            <button onClick={() => editorRef.current && openSearchPanel(editorRef.current)} type="button">
              {t("contracts.artifact.searchCode")}
            </button>
            <button aria-pressed={wrap} onClick={() => setWrap((current) => !current)} type="button">
              {t("contracts.artifact.wrapLines")}
            </button>
            <CopyButton className="source-toolbar-copy" value={selected.content} />
            {fullscreenAvailable ? (
              <button aria-pressed={fullscreen} onClick={() => void toggleFullscreen()} type="button">
                {fullscreen ? t("contracts.artifact.exitFullscreen") : t("contracts.artifact.fullscreen")}
              </button>
            ) : null}
          </div>
        </div>
        <div className="source-editor" data-read-only="true" ref={hostRef} />
        <footer className="source-editor-status">
          <span>{language.toUpperCase()}</span>
          <span>{selected.content.split("\n").length} {t("contracts.artifact.lines")}</span>
          <span>{t("contracts.artifact.readOnly")}</span>
        </footer>
      </div>
    </div>
  );
}

function CompilerSettings({
  libraries,
  raw,
  sourceCount,
  summary,
}: {
  libraries: Record<string, string>;
  raw: Record<string, unknown>;
  sourceCount: number;
  summary: CompilerSettingsSummary;
}) {
  const { t } = useTranslation();
  const explicit = (value: string | number | boolean | undefined) =>
    value === undefined ? t("contracts.artifact.compilerDefault") : String(value);
  const optimizer = summary.optimizerEnabled === undefined
    ? t("contracts.artifact.compilerDefault")
    : summary.optimizerEnabled
      ? t("contracts.artifact.enabled")
      : t("contracts.artifact.disabled");
  const booleanSetting = (value: boolean | undefined) => value === undefined
    ? t("contracts.artifact.compilerDefault")
    : value
      ? t("contracts.artifact.enabled")
      : t("contracts.artifact.disabled");
  const optimizerSettings = isRecord(raw.optimizer) ? raw.optimizer : undefined;
  const optimizerDetails = optimizerSettings && isRecord(optimizerSettings.details)
    ? optimizerSettings.details
    : undefined;
  const outputSelection = isRecord(raw.outputSelection) ? raw.outputSelection : undefined;
  const modelChecker = isRecord(raw.modelChecker) ? raw.modelChecker : undefined;
  const metadataValue = summary.metadata.length > 0
    ? t("contracts.artifact.explicitFields", { count: summary.metadata.length })
    : t("contracts.artifact.compilerDefault");
  const remappingsValue = summary.remappings.length > 0
    ? t("contracts.artifact.explicitEntries", { count: summary.remappings.length })
    : t("contracts.artifact.compilerDefault");

  return (
    <section className="artifact-section" aria-labelledby="compiler-settings-title">
      <div className="artifact-section-heading">
        <div>
          <span className="eyebrow">{t("contracts.artifact.configurationEyebrow")}</span>
          <h3 id="compiler-settings-title">{t("contracts.settings")}</h3>
        </div>
      </div>
      <dl className="compiler-settings-grid">
        <SummaryFact label={t("contracts.artifact.optimizer")} value={optimizer} />
        <SummaryFact label={t("contracts.artifact.optimizerRuns")} value={explicit(summary.optimizerRuns)} />
        <SummaryFact label={t("contracts.artifact.evmVersion")} value={explicit(summary.evmVersion)} />
        <SummaryFact label={t("contracts.artifact.viaIR")} value={booleanSetting(summary.viaIR)} />
        <SummaryFact label={t("contracts.artifact.metadata")} value={metadataValue} />
        <SummaryFact label={t("contracts.artifact.remappings")} value={remappingsValue} />
        <SummaryFact label={t("contracts.artifact.sourceCount")} value={String(sourceCount)} />
        <SummaryFact label={t("contracts.artifact.libraryCount")} value={String(Object.keys(libraries).length)} />
      </dl>
      {summary.metadata.length > 0 ? (
        <div className="compiler-setting-group">
          <h4>{t("contracts.artifact.metadata")}</h4>
          <dl>
            {summary.metadata.map((item) => <SummaryFact key={item.label} label={item.label} value={item.value} />)}
          </dl>
        </div>
      ) : null}
      {summary.remappings.length > 0 ? (
        <div className="compiler-setting-group">
          <h4>{t("contracts.artifact.remappings")}</h4>
          <ul className="setting-chips">
            {summary.remappings.map((remapping) => <li key={remapping}><code>{remapping}</code></li>)}
          </ul>
        </div>
      ) : null}
      <div className="artifact-disclosure-list compiler-setting-disclosures">
        {optimizerDetails ? (
          <ArtifactDisclosure title={t("contracts.artifact.optimizerDetails")} value={optimizerDetails} />
        ) : null}
        {outputSelection ? (
          <ArtifactDisclosure title={t("contracts.artifact.outputSelection")} value={outputSelection} />
        ) : null}
        {modelChecker ? (
          <ArtifactDisclosure title={t("contracts.artifact.modelChecker")} value={modelChecker} />
        ) : null}
        {Object.keys(libraries).length > 0 ? (
          <ArtifactDisclosure title={t("contracts.artifact.linkedLibraries")} value={libraries} />
        ) : null}
        <ArtifactDisclosure title={t("contracts.artifact.rawSettings")} value={raw} />
      </div>
    </section>
  );
}

function SummaryFact({
  label,
  mono,
  value,
  wide,
}: {
  label: string;
  mono?: boolean;
  value: string;
  wide?: boolean;
}) {
  return (
    <div className={wide ? "wide" : undefined}>
      <dt>{label}</dt>
      <dd className={mono ? "mono-wrap" : undefined}>{value}</dd>
    </div>
  );
}

function ConstructorArgumentsDisclosure({
  abi,
  encoded,
}: {
  abi: unknown;
  encoded: string;
}) {
  const { t } = useTranslation();
  const decoded = useMemo(() => {
    try {
      return decodeConstructorArguments(abi, encoded);
    } catch {
      return undefined;
    }
  }, [abi, encoded]);
  const byteCount = Math.floor(Math.max(0, encoded.replace(/^0x/u, "").length / 2));

  return (
    <details className="artifact-disclosure">
      <summary>
        <span>
          <strong>{t("contracts.artifact.constructorArguments")}</strong>
          <small>
            {decoded
              ? t("contracts.artifact.constructorDecodedSummary", {
                bytes: byteCount,
                count: decoded.length,
              })
              : t("contracts.artifact.hexBytes", { count: byteCount })}
          </small>
        </span>
        <span aria-hidden="true">＋</span>
      </summary>
      <div className="artifact-disclosure-body constructor-arguments-body">
        {decoded ? (
          <section
            aria-label={t("contracts.artifact.constructorDecoded")}
            className="constructor-arguments-decoded"
          >
            <strong>{t("contracts.artifact.constructorDecoded")}</strong>
            {decoded.length === 0 ? (
              <p className="quiet">{t("contracts.artifact.constructorNoParameters")}</p>
            ) : (
              <div className="constructor-argument-list">
                {decoded.map((argument) => (
                  <div className="constructor-argument-row" key={argument.index}>
                    <small>{argument.name || `#${argument.index}`} · {argument.type}</small>
                    <code>{argument.display}</code>
                  </div>
                ))}
              </div>
            )}
          </section>
        ) : (
          <p className="form-error constructor-arguments-warning" role="status">
            {t("contracts.artifact.constructorDecodeUnavailable")}
          </p>
        )}
        <section className="constructor-arguments-raw" aria-label={t("contracts.artifact.constructorRaw")}>
          <div className="constructor-arguments-raw-heading">
            <strong>{t("contracts.artifact.constructorRaw")}</strong>
            <CopyButton value={encoded} />
          </div>
          <pre tabIndex={0}>{encoded}</pre>
        </section>
      </div>
    </details>
  );
}

function ArtifactDisclosure({
  description,
  rawText,
  title,
  value,
}: {
  description?: string;
  rawText?: string;
  title: string;
  value?: unknown;
}) {
  const text = rawText ?? JSON.stringify(value, null, 2);
  return (
    <details className="artifact-disclosure">
      <summary>
        <span>
          <strong>{title}</strong>
          {description ? <small>{description}</small> : null}
        </span>
        <span aria-hidden="true">＋</span>
      </summary>
      <div className="artifact-disclosure-body">
        <div className="artifact-disclosure-actions"><CopyButton value={text} /></div>
        <pre tabIndex={0}>{text}</pre>
      </div>
    </details>
  );
}
