import { FileCode2, FileJson2, Upload, Check } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { UploadedFile } from "./verificationFiles";

export function VerificationFileUpload({
  id,
  label,
  files,
  extensions,
  single = false,
  editablePaths = false,
  disabled,
  onSelect,
  onRemove,
  onRename,
}: {
  id: string;
  label: string;
  files: UploadedFile[];
  extensions: string[];
  single?: boolean;
  editablePaths?: boolean;
  disabled: boolean;
  onSelect: (files: File[]) => void;
  onRemove: (index: number) => void;
  onRename?: (index: number, path: string) => void;
}) {
  const { t } = useTranslation();
  const Icon = single ? FileJson2 : FileCode2;
  return (
    <div className="verification-upload">
      <label className="sourcify-upload-card" data-disabled={disabled} htmlFor={id}>
        <input
          id={id}
          type="file"
          disabled={disabled}
          multiple={!single}
          accept={extensions.join(",")}
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
        <strong id={`${id}-title`}>{label}</strong>
        <span className="quiet" id={`${id}-help`}>
          {t("verificationUpload.allowed", { extensions: extensions.join(", ") })}
        </span>
        <span className="sourcify-upload-action">
          <Upload aria-hidden="true" />
          {t(files.length ? "sourcify.replaceFiles" : "sourcify.chooseFiles")}
        </span>
        <span className="sourcify-upload-status" role="status">
          {files.length ? (
            <>
              <Check aria-hidden="true" />
              {t("verificationUpload.selected", { count: files.length })}
            </>
          ) : (
            t("sourcify.noFileSelected")
          )}
        </span>
      </label>
      {files.length > 0 && (
        <ul
          className="verification-upload-list"
          aria-label={t("verificationUpload.list", { label })}
        >
          {files.map((file, index) => (
            <li key={index}>
              {editablePaths ? (
                <label className="field-control">
                  <span>{t("verificationUpload.path", { name: file.name })}</span>
                  <input
                    value={file.path}
                    disabled={disabled}
                    spellCheck={false}
                    onChange={(event) => onRename?.(index, event.target.value)}
                  />
                </label>
              ) : (
                <span className="verification-upload-name">{file.name}</span>
              )}
              <button
                className="button"
                type="button"
                disabled={disabled}
                aria-label={t("verificationUpload.removeNamed", { name: file.path })}
                onClick={() => onRemove(index)}
              >
                {t("verificationUpload.remove")}
              </button>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
