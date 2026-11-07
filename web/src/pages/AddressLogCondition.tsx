import { Label } from "@/components/ui/label";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useTranslation } from "react-i18next";
import type { AddressLogEvent } from "@/api/heyapi/types.gen";
import { SelectControl } from "@/components/SelectControl";
import { AbiInputEditor } from "@/contracts/AbiInputEditor";
import { createAbiInputTree } from "@/contracts/abi";
import { encodeIndexedTopics } from "@/contracts/eventTopics";
import { editableEventParameter, newLogCondition, type LogCondition } from "./logFilterState";

export function AddressLogCondition({
  condition,
  event,
  choices,
  id,
  invalid,
  onChange,
  onRemove,
  onError,
}: {
  condition: LogCondition;
  event?: AddressLogEvent;
  choices: number[];
  id: string;
  invalid: boolean;
  onChange: (condition: LogCondition) => void;
  onRemove: () => void;
  onError: () => void;
}) {
  const { t } = useTranslation();
  const field = event?.indexed_fields.find((item) => item.topic_index === condition.position);
  const setPosition = (position: number) => {
    try {
      const next = event?.indexed_fields.find((item) => item.topic_index === position);
      onChange(newLogCondition(condition.id, position, next));
    } catch {
      onError();
    }
  };
  const parameter = editableEventParameter(field);
  let editor;
  let error = false;
  try {
    if (condition.format === "raw" && field && condition.node && parameter) {
      editor = (
        <AbiInputEditor
          parameter={parameter}
          node={condition.node}
          path={[field.parameter_index]}
          formID={id}
          disabled={false}
          walletShortcut={false}
          onChange={(node) => onChange({ ...condition, node })}
          onLimitError={onError}
        />
      );
      if (invalid) encodeIndexedTopics([parameter], [condition.node]);
    }
  } catch {
    error = true;
  }
  if (invalid && condition.format === "hash" && !/^0x[0-9a-fA-F]{64}$/u.test(condition.hash.trim()))
    error = true;
  return (
    <div className="address-log-condition">
      <div className="address-log-condition-heading">
        <Label>
          {t(event ? "addressLogs.indexedField" : "addressLogs.position")}
          <SelectControl
            value={condition.position}
            onChange={(e) => setPosition(Number(e.target.value))}
          >
            {[condition.position, ...choices].sort().map((position) => {
              const item = event?.indexed_fields.find(
                (candidate) => candidate.topic_index === position,
              );
              return (
                <option value={position} key={position}>
                  {item
                    ? `${item.name || `#${item.parameter_index}`} (${item.type}, topic${position})`
                    : `topic${position}`}
                </option>
              );
            })}
          </SelectControl>
        </Label>
        {field && (
          <Label>
            {t("addressLogs.valueFormat")}
            <SelectControl
              value={condition.format}
              onChange={(e) => {
                try {
                  const format = e.target.value as "raw" | "hash";
                  onChange({
                    ...condition,
                    format,
                    node:
                      format === "raw"
                        ? (condition.node ?? createAbiInputTree([parameter!])[0])
                        : condition.node,
                  });
                } catch {
                  onError();
                }
              }}
            >
              <option value="raw" disabled={!parameter}>
                {t("addressLogs.rawValue")}
              </option>
              <option value="hash">{t("addressLogs.topic")}</option>
            </SelectControl>
          </Label>
        )}
        <Button
          variant="outline"
          type="button"
          data-remove-condition
          aria-label={`${t("addressLogs.removeCondition")} ${field?.name || `topic${condition.position}`}`}
          onClick={onRemove}
        >
          {t("addressLogs.removeCondition")}
        </Button>
      </div>
      {editor ?? (
        <Label>
          {t("addressLogs.topic")}
          <Input
            value={condition.hash}
            maxLength={66}
            spellCheck={false}
            aria-invalid={error}
            onChange={(e) => onChange({ ...condition, hash: e.target.value })}
            placeholder="0x…"
          />
        </Label>
      )}
      {error && <p className="address-log-error">{t("addressLogs.invalidValue")}</p>}
    </div>
  );
}
