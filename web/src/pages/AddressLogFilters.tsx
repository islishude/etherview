import { useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { useAddressLogDefinition, type AddressLogRange } from "@/api/addressLogs";
import { AddressLogCondition } from "./AddressLogCondition";
import { assertAbiInputTreeWithinLimits } from "@/contracts/abi";
import { EventNameSelect } from "./EventNameSelect";
import { newLogCondition, type LogCondition, type LogDraft } from "./logFilterState";

export function AddressLogFilters({
  address,
  draft,
  range,
  disabled,
  invalid,
  onChange,
}: {
  address: string;
  draft: LogDraft;
  range: AddressLogRange;
  disabled: boolean;
  invalid: boolean;
  onChange: (draft: LogDraft) => void;
}) {
  const { t } = useTranslation();
  const id = useId();
  const add = useRef<HTMLButtonElement>(null);
  const root = useRef<HTMLDivElement>(null);
  const [error, setError] = useState("");
  const focus = useRef<{ kind: "add" | "remove"; index: number } | undefined>(undefined);
  useLayoutEffect(() => {
    const pending = focus.current;
    if (!pending) return;
    focus.current = undefined;
    if (pending.kind === "add")
      root.current
        ?.querySelectorAll<HTMLElement>(".address-log-condition")
        .item(pending.index)
        ?.querySelector<HTMLElement>("input,select,textarea")
        ?.focus();
    else {
      const buttons = root.current?.querySelectorAll<HTMLButtonElement>("[data-remove-condition]");
      (buttons?.[Math.min(pending.index, (buttons?.length ?? 1) - 1)] ?? add.current)?.focus();
    }
  }, [draft.conditions.length]);
  const definition = useAddressLogDefinition(address, draft.event ? undefined : draft.eventID);
  const event =
    draft.event ?? definition.data?.data.find((item) => item.event_id === draft.eventID);
  useEffect(() => {
    if (event && !draft.event) onChange({ ...draft, event });
  }, [event, draft, onChange]);
  const choices =
    draft.mode === "topic"
      ? [0, 1, 2, 3]
      : (event?.indexed_fields.map((field) => field.topic_index) ?? []);
  const remaining = choices.filter(
    (position) => !draft.conditions.some((item) => item.position === position),
  );
  const change = (index: number, next: LogCondition) => {
    try {
      const conditions = draft.conditions.map((item, i) => (i === index ? next : item));
      assertAbiInputTreeWithinLimits(conditions.flatMap((item) => (item.node ? [item.node] : [])));
      onChange({ ...draft, conditions });
      setError("");
    } catch {
      setError(t("addressLogs.invalidValue"));
    }
  };
  const remove = (index: number) => {
    focus.current = { kind: "remove", index };
    onChange({ ...draft, conditions: draft.conditions.filter((_, i) => i !== index) });
    setError("");
  };
  return (
    <div className="address-log-filter-editor" ref={root}>
      {draft.mode === "name" && (
        <EventNameSelect
          address={address}
          value={draft.eventID}
          label={event?.signature ?? draft.signature}
          range={range}
          disabled={disabled}
          onChange={(next) => {
            onChange({
              ...draft,
              event: next,
              eventID: next?.event_id ?? "",
              signature: "",
              conditions: [],
            });
            setError("");
          }}
        />
      )}
      {draft.mode === "name" && draft.eventID && !event && (
        <p role="status">
          {t(definition.isPending ? "addressLogs.loading" : "addressLogs.definitionUnavailable")}
        </p>
      )}
      {draft.conditions.map((condition, index) => (
        <AddressLogCondition
          key={condition.id}
          condition={condition}
          event={event}
          choices={remaining}
          id={`${id}-${condition.id}`}
          invalid={invalid}
          onChange={(next) => change(index, next)}
          onRemove={() => remove(index)}
          onError={() => setError(t("addressLogs.invalidValue"))}
        />
      ))}
      <div className="address-log-add-condition">
        {remaining.length > 0 && (
          <button
            ref={add}
            type="button"
            onClick={() => {
              try {
                const position = remaining[0]!;
                const field = event?.indexed_fields.find((item) => item.topic_index === position);
                const condition = newLogCondition(
                  Math.max(-1, ...draft.conditions.map((item) => item.id)) + 1,
                  position,
                  field,
                );
                const conditions = [...draft.conditions, condition];
                assertAbiInputTreeWithinLimits(
                  conditions.flatMap((item) => (item.node ? [item.node] : [])),
                );
                focus.current = { kind: "add", index: conditions.length - 1 };
                onChange({ ...draft, conditions });
                setError("");
              } catch {
                setError(t("addressLogs.invalidValue"));
              }
            }}
          >
            {t("addressLogs.addCondition")}
          </button>
        )}
      </div>
      {error && (
        <p role="alert" className="address-log-error">
          {error}
        </p>
      )}
    </div>
  );
}
