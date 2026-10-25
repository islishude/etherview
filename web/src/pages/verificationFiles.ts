import { useEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import type { TFunction } from "i18next";

export const MAX_UPLOAD_BYTES = 5 * 1024 * 1024;
export const MAX_UPLOAD_FILES = 1024;
export const sourceExtensions = {
  solidity: [".sol"],
  yul: [".yul"],
  vyper: [".vy", ".vyi"],
  geas: [".eas"],
  sourcify: [".sol", ".yul", ".vy", ".vyi"],
};
export const byteLength = (value: string) => new TextEncoder().encode(value).byteLength;
export type UploadedFile = { name: string; path: string; content: string; size: number };
export const fileMap = (files: UploadedFile[]): Record<string, string> =>
  Object.fromEntries(files.map((file) => [file.path, file.content]));

export function validateUploadPaths(
  files: Pick<UploadedFile, "path">[],
  extensions: string[],
  t: TFunction,
): string | undefined {
  const seen = new Set<string>();
  for (const { path } of files) {
    if (
      !path ||
      byteLength(path) > 384 ||
      /[\\\x00-\x1f\x7f]/.test(path) ||
      path.split("/").some((part) => !part || part === "." || part === "..")
    )
      return t("verificationUpload.invalidPath", { name: path });
    if (!extensions.some((extension) => path.endsWith(extension)))
      return t("verificationUpload.invalidExtension", {
        name: path,
        extensions: extensions.join(", "),
      });
    if (seen.has(path)) return t("verificationUpload.duplicatePath", { name: path });
    seen.add(path);
  }
}

export function useVerificationFiles({
  extensions,
  single = false,
  onChange,
  validateContent,
}: {
  extensions: string[];
  single?: boolean;
  onChange: () => void;
  validateContent?: (content: string) => string | undefined;
}) {
  const { t } = useTranslation();
  const [files, setFiles] = useState<UploadedFile[]>([]);
  const [reading, setReading] = useState(false);
  const [readError, setReadError] = useState<string>();
  const revision = useRef(0);
  useEffect(
    () => () => {
      revision.current++;
    },
    [],
  );
  const error = readError ?? validateUploadPaths(files, extensions, t);

  const changed = () => {
    revision.current++;
    setReading(false);
    setReadError(undefined);
    onChange();
  };
  const clear = () => {
    changed();
    setFiles([]);
  };
  const remove = (index: number) => {
    changed();
    setFiles((current) => current.filter((_, position) => position !== index));
  };
  const rename = (index: number, path: string) => {
    changed();
    setFiles((current) =>
      current.map((file, position) => (position === index ? { ...file, path } : file)),
    );
  };
  const select = async (selected: File[], retained: UploadedFile[] = []) => {
    if (!selected.length) return;
    changed();
    const currentRevision = revision.current;
    if (
      (single && selected.length !== 1) ||
      selected.length + retained.length > MAX_UPLOAD_FILES ||
      selected.reduce((total, file) => total + file.size, 0) +
        retained.reduce((total, file) => total + file.size, 0) >
        MAX_UPLOAD_BYTES
    ) {
      setReadError(t("verificationUpload.limit"));
      return;
    }
    const paths = selected.map((file) => ({ path: file.webkitRelativePath || file.name }));
    const pathError = validateUploadPaths(paths, extensions, t);
    if (pathError) {
      setReadError(pathError);
      return;
    }
    setReading(true);
    try {
      const contents = await Promise.all(selected.map((file) => file.text()));
      if (revision.current !== currentRevision) return;
      const next = selected.map((file, index) => ({
        name: file.name,
        path: paths[index]!.path,
        content: contents[index]!,
        size: file.size,
      }));
      for (const file of next) {
        const contentError = validateContent?.(file.content);
        if (contentError) {
          setReadError(contentError);
          return;
        }
      }
      if (
        [...retained, ...next].reduce((total, file) => total + byteLength(file.content), 0) >
        MAX_UPLOAD_BYTES
      ) {
        setReadError(t("verificationUpload.limit"));
        return;
      }
      setFiles(next);
    } catch {
      if (revision.current === currentRevision) setReadError(t("verificationUpload.readFailed"));
    } finally {
      if (revision.current === currentRevision) setReading(false);
    }
  };
  return { files, reading, error, select, clear, remove, rename };
}
