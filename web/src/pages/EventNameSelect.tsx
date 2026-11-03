import { UIIcon } from "@/components/UIIcon";
import { useEffect, useId, useRef, useState } from "react";
import { Check, ChevronDown, Search } from "lucide-react";
import { useTranslation } from "react-i18next";
import { useAddressLogEvents, type AddressLogRange } from "@/api/addressLogs";
import { useBrowsingCursorHistory } from "@/api/pagination";
import { BrowsingScope } from "@/components/BrowsingScope";
import { QueryNotice } from "@/components/QueryNotice";

export function EventNameSelect({
  address,
  value,
  range,
  disabled,
  onChange,
}: {
  address: string;
  value: string;
  range: AddressLogRange;
  disabled: boolean;
  onChange: (value: string) => void;
}) {
  const { t } = useTranslation();
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const trigger = useRef<HTMLButtonElement>(null);
  const close = () => {
    setOpen(false);
    trigger.current?.focus();
  };
  useEffect(() => {
    if (!open) return;
    const dismiss = (event: PointerEvent) => {
      if (event.target instanceof Node && !root.current?.contains(event.target)) setOpen(false);
    };
    document.addEventListener("pointerdown", dismiss);
    return () => document.removeEventListener("pointerdown", dismiss);
  }, [open]);
  return (
    <div
      className="event-name-select"
      ref={root}
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) setOpen(false);
      }}
      onKeyDown={(event) => {
        if (event.nativeEvent.isComposing) return;
        if (event.key === "Escape" && open) {
          event.preventDefault();
          event.stopPropagation();
          close();
        }
      }}
    >
      <span className="address-log-field-label">{t("addressLogs.event")}</span>
      <button
        className="event-name-trigger"
        ref={trigger}
        type="button"
        disabled={disabled}
        aria-label={`${t("addressLogs.event")}: ${value || t("addressLogs.all")}`}
        aria-haspopup="listbox"
        aria-expanded={open && !disabled}
        onClick={() => setOpen(!open)}
      >
        <span>{value || t("addressLogs.all")}</span>
        <UIIcon icon={ChevronDown} size={16} />
      </button>
      {open && !disabled && (
        <EventOptions
          key={JSON.stringify([address, range])}
          address={address}
          range={range}
          value={value}
          choose={(next) => {
            onChange(next);
            close();
          }}
        />
      )}
    </div>
  );
}
function EventOptions({
  address,
  range,
  value,
  choose,
}: {
  address: string;
  range: AddressLogRange;
  value: string;
  choose: (value: string) => void;
}) {
  const { t } = useTranslation();
  const [search, setSearch] = useState("");
  const [name, setName] = useState("");
  const [composing, setComposing] = useState(false);
  const [active, setActive] = useState(0);
  const input = useRef<HTMLInputElement>(null);
  const list = useRef<HTMLDivElement>(null);
  const id = useId();
  useEffect(() => {
    input.current?.focus();
  }, []);
  useEffect(() => {
    if (composing) return;
    const timer = setTimeout(() => {
      setName(search.trim());
      setActive(0);
    }, 250);
    return () => clearTimeout(timer);
  }, [search, composing]);
  const pager = useBrowsingCursorHistory(
    `address-log-events:${address}:${JSON.stringify(range)}:${name}`,
  );
  const events = useAddressLogEvents(address, name, range, pager.request);
  const reset = () => {
    input.current?.focus();
    pager.reset();
  };
  const options = [
    { name: t("addressLogs.all"), signature: "" },
    ...Array.from(
      new Map(
        (events.data?.pages.flatMap((p) => p.data) ?? []).map((item) => [item.signature, item]),
      ).values(),
    ),
  ];
  const selected = Math.min(active, options.length - 1);
  useEffect(() => {
    list.current?.children[selected]?.scrollIntoView?.({ block: "nearest" });
  }, [selected]);
  return (
    <div className="event-name-popup">
      <label className="event-name-search">
        <UIIcon icon={Search} size={16} />
        <input
          ref={input}
          role="combobox"
          aria-expanded="true"
          aria-autocomplete="list"
          aria-controls={id}
          aria-activedescendant={`${id}-${selected}`}
          aria-label={t("addressLogs.nameSearch")}
          placeholder={t("addressLogs.nameSearch")}
          value={search}
          maxLength={128}
          onChange={(e) => setSearch(e.target.value)}
          onCompositionStart={() => setComposing(true)}
          onCompositionEnd={() => setComposing(false)}
          onKeyDown={(event) => {
            if (event.nativeEvent.isComposing || composing) return;
            if (event.key === "ArrowDown" || event.key === "ArrowUp") {
              event.preventDefault();
              setActive(
                Math.max(
                  0,
                  Math.min(options.length - 1, selected + (event.key === "ArrowDown" ? 1 : -1)),
                ),
              );
            }
            if (event.key === "Enter") {
              event.preventDefault();
              if (options[selected]) choose(options[selected].signature);
            }
          }}
        />
      </label>
      <BrowsingScope status={events.data?.pages[0]?.meta.browsing}>
        <QueryNotice loading={events.isPending} error={events.error} onReset={reset} />
        <div
          className="event-name-options"
          id={id}
          ref={list}
          role="listbox"
          aria-label={t("addressLogs.event")}
        >
          {options.map((option, index) => (
            <button
              type="button"
              role="option"
              id={`${id}-${index}`}
              key={option.signature}
              tabIndex={-1}
              aria-selected={value === option.signature}
              data-active={selected === index}
              onPointerDown={(event) => event.preventDefault()}
              onClick={() => choose(option.signature)}
            >
              <span>
                <strong>{option.name}</strong>
                {option.signature && <code>{option.signature}</code>}
              </span>
              {value === option.signature && <UIIcon icon={Check} size={16} />}
            </button>
          ))}
        </div>
        {!events.isPending && !events.error && options.length === 1 && (
          <p role="status">{t("addressLogs.noNames")}</p>
        )}
        {events.error && (
          <button type="button" onClick={reset}>
            {t("addressLogs.retry")}
          </button>
        )}
        {events.hasNextPage && (
          <button
            className="event-name-more"
            type="button"
            disabled={events.isFetchingNextPage || pager.invalid}
            onClick={() => {
              input.current?.focus();
              void events.fetchNextPage();
            }}
          >
            {t(events.isFetchingNextPage ? "addressLogs.loading" : "addressLogs.loadMore")}
          </button>
        )}
        <p className="event-name-note">{t("addressLogs.directoryNote")}</p>
      </BrowsingScope>
    </div>
  );
}
