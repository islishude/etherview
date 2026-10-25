import userEvent from "@testing-library/user-event";

// jsdom's File does not implement Blob.text; keep real File selection semantics.
export function textFile(name: string, content: string, type = "text/plain") {
  const file = new File([content], name, { type });
  Object.defineProperty(file, "text", { value: async () => content });
  return file;
}

export async function uploadFiles(input: HTMLElement, files: Record<string, string>) {
  await userEvent.setup({ applyAccept: false }).upload(
    input,
    Object.entries(files).map(([name, content]) => textFile(name, content)),
  );
}
