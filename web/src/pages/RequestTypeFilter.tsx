import { UIIcon } from "@/components/UIIcon";
import { useEffect, useId, useRef, useState } from "react";
import { Check, ChevronDown, ListFilter, Search } from "lucide-react";
import { useTranslation } from "react-i18next";
import type { ExecutionRequestType } from "@/api/heyapi/types.gen";
import { formatInteger } from "@/components/format";

const requestTypes = ["0x00", "0x01", "0x02", "0x03", "0x04"] as const;
type Selection = ExecutionRequestType | "";

export function RequestTypeFilter({
  value,
  counts,
  total,
  onChange,
}: {
  value: Selection;
  counts: Record<string, string> | undefined;
  total: string | undefined;
  onChange: (value: Selection) => void;
}) {
  const { t, i18n } = useTranslation();
  const locale = i18n.resolvedLanguage ?? "en";
  const [open, setOpen] = useState(false);
  const [search, setSearch] = useState("");
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const searchInput = useRef<HTMLInputElement>(null);
  const id = useId();
  const label = (type: Selection) => (type ? t(`requests.types.${type}`) : t("requests.all"));
  const count = (type: Selection) => formatInteger(type ? (counts?.[type] ?? "0") : total, locale);
  const matches = requestTypes.filter((type) =>
    `${type} ${label(type)}`
      .toLocaleLowerCase(locale)
      .includes(search.trim().toLocaleLowerCase(locale)),
  );

  useEffect(() => {
    if (!open) return;
    searchInput.current?.focus();
    const dismiss = (event: PointerEvent) => {
      if (event.target instanceof Node && !root.current?.contains(event.target)) setOpen(false);
    };
    document.addEventListener("pointerdown", dismiss);
    return () => document.removeEventListener("pointerdown", dismiss);
  }, [open]);

  return (
    <div
      className="request-type-filter"
      ref={root}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false);
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape" && open) {
          event.preventDefault();
          event.stopPropagation();
          setOpen(false);
          trigger.current?.focus();
        }
      }}
    >
      <button
        type="button"
        className="request-filter-trigger"
        ref={trigger}
        aria-expanded={open}
        aria-controls={id}
        aria-label={`${t("requests.filter")}: ${label(value)}, ${count(value)}`}
        onClick={() => {
          setSearch("");
          setOpen(!open);
        }}
      >
        <UIIcon icon={ListFilter} size={16} />
        <span>{label(value)}</span>
        <span className="request-filter-count">{count(value)}</span>
        <UIIcon icon={ChevronDown} size={16} />
      </button>
      {open && (
        <div className="request-filter-popover" id={id}>
          <label className="request-filter-search">
            <UIIcon icon={Search} size={16} />
            <input
              ref={searchInput}
              type="search"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
              aria-label={t("requests.searchTypes")}
              placeholder={t("requests.searchTypes")}
            />
          </label>
          <div className="request-filter-options" role="group" aria-label={t("requests.filter")}>
            {(["", ...matches] as Selection[]).map((type) => (
              <button
                key={type}
                type="button"
                aria-pressed={value === type}
                onClick={() => {
                  setOpen(false);
                  trigger.current?.focus();
                  onChange(type);
                }}
              >
                <span className="request-filter-check">
                  {value === type && <UIIcon icon={Check} size={16} />}
                </span>
                <span>{label(type)}</span> {type && <code>{type}</code>}{" "}
                <span className="request-filter-count">{count(type)}</span>
              </button>
            ))}
            {matches.length === 0 && <p role="status">{t("requests.noTypes")}</p>}
          </div>
        </div>
      )}
    </div>
  );
}
