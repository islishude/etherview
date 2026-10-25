import { FileCode2, FileJson2, Upload, Check } from "lucide-react";
import { useTranslation } from "react-i18next";

export function SourcifyFileUpload({
  kind,
  names,
  disabled,
  onSelect,
}: {
  kind: "sources" | "metadata";
  names: string[];
  disabled: boolean;
  onSelect: (files: File[]) => void;
}) {
  const { t } = useTranslation();
  const metadata = kind === "metadata";
  const Icon = metadata ? FileJson2 : FileCode2;
  const id = `sourcify-upload-${kind}`;
  return (
    <label className="sourcify-upload-card" data-disabled={disabled} htmlFor={id}>
      <input
        id={id}
        type="file"
        disabled={disabled}
        multiple={!metadata}
        accept={metadata ? ".json,application/json" : undefined}
        aria-labelledby={`${id}-title`}
        aria-describedby={`${id}-help`}
        onChange={(event) => {
          onSelect(Array.from(event.target.files ?? []));
          event.target.value = "";
        }}
      />
      <span className="sourcify-upload-icon">
        <Icon aria-hidden="true" />
      </span>
      <strong id={`${id}-title`}>
        {t(metadata ? "sourcify.selectMetadata" : "sourcify.selectSources")}
      </strong>
      <span className="quiet" id={`${id}-help`}>
        {t(metadata ? "sourcify.metadataHelp" : "sourcify.sourcesHelp")}
      </span>
      <span className="sourcify-upload-action">
        <Upload aria-hidden="true" />
        {t(names.length ? "sourcify.replaceFiles" : "sourcify.chooseFiles")}
      </span>
      <span className="sourcify-upload-status" role="status">
        {names.length > 0 ? (
          <>
            <Check aria-hidden="true" />
            {metadata ? "metadata.json" : t("sourcify.selectedSources", { count: names.length })}
          </>
        ) : (
          t("sourcify.noFileSelected")
        )}
      </span>
      {!metadata && names.length > 0 && (
        <span className="sourcify-upload-names">
          {names.slice(0, 3).join(", ")}
          {names.length > 3 ? "…" : ""}
        </span>
      )}
    </label>
  );
}
