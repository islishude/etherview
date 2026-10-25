import { useEffect, useId, useRef, useState } from "react";
import { ChevronDown } from "lucide-react";
import { useTranslation } from "react-i18next";

export function CompilerVersionSelect({
  id,
  versions,
  value,
  onChange,
  disabled,
}: {
  id: string;
  versions: readonly string[];
  value: string;
  onChange: (version: string) => void;
  disabled: boolean;
}) {
  const { t } = useTranslation();
  const listID = useId();
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const [activeIndex, setActiveIndex] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  const listRef = useRef<HTMLUListElement>(null);
  const query = search.trim().toLowerCase();
  const filtered = versions.filter((version) => version.toLowerCase().includes(query));
  const expanded = open && !disabled;
  const active = Math.min(activeIndex, filtered.length - 1);

  useEffect(() => {
    // Closing keeps focus in the field; the next typed or pasted query replaces
    // the displayed selection instead of being appended to the compiler version.
    if (!expanded && document.activeElement === inputRef.current) inputRef.current?.select();
  }, [expanded, value]);

  useEffect(() => {
    if (expanded) {
      listRef.current?.children[active]?.scrollIntoView?.({ block: "nearest" });
    }
  }, [expanded, active, search]);

  const close = () => {
    setOpen(false);
    setSearch("");
  };
  const expand = () => {
    if (open) return;
    setSearch("");
    setActiveIndex(Math.max(0, versions.indexOf(value)));
    setOpen(true);
  };
  const choose = (version: string) => {
    onChange(version);
    close();
  };

  return (
    <div className="compiler-version-select" onBlur={close}>
      <input
        id={id}
        ref={inputRef}
        role="combobox"
        aria-autocomplete="list"
        aria-expanded={expanded}
        aria-controls={expanded ? listID : undefined}
        aria-activedescendant={expanded && active >= 0 ? `${listID}-${active}` : undefined}
        autoComplete="off"
        spellCheck={false}
        disabled={disabled}
        value={expanded ? search : value}
        placeholder={t("verification.searchVersions")}
        onFocus={expand}
        onClick={expand}
        onChange={(event) => {
          setSearch(event.target.value);
          setActiveIndex(0);
          setOpen(true);
        }}
        onKeyDown={(event) => {
          if (event.nativeEvent.isComposing) return;
          if (event.key === "ArrowDown" || event.key === "ArrowUp") {
            event.preventDefault();
            if (!expanded) expand();
            else {
              const direction = event.key === "ArrowDown" ? 1 : -1;
              setActiveIndex(Math.max(0, Math.min(filtered.length - 1, active + direction)));
            }
          } else if (event.key === "Enter") {
            event.preventDefault();
            if (!expanded) expand();
            else if (filtered[active]) choose(filtered[active]);
          } else if (event.key === "Escape") {
            event.preventDefault();
            close();
          } else if (event.key === "Tab") close();
        }}
      />
      <ChevronDown className="compiler-version-chevron" aria-hidden="true" />
      {expanded && (
        <div className="compiler-version-popup">
          <ul
            id={listID}
            ref={listRef}
            role="listbox"
            aria-label={t("verification.compilerVersion")}
          >
            {filtered.map((version, index) => (
              <li
                id={`${listID}-${index}`}
                key={version}
                role="option"
                aria-selected={version === value}
                data-active={index === active}
                onPointerDown={(event) => event.preventDefault()}
                onClick={() => choose(version)}
              >
                {version}
              </li>
            ))}
          </ul>
          {filtered.length === 0 && <p role="status">{t("verification.noMatchingVersions")}</p>}
        </div>
      )}
    </div>
  );
}
