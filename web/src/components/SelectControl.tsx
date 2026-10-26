import "./SelectControl.css";
import { ChevronDown } from "lucide-react";
import type { ComponentPropsWithRef } from "react";
import { UIIcon } from "./UIIcon";

/** Keep native selection, labels, keyboard behavior and form submission. */
export function SelectControl(props: ComponentPropsWithRef<"select">) {
  if (props.multiple || props.size) return <select {...props} />;
  return (
    <span className="select-control">
      <select {...props} />
      <UIIcon icon={ChevronDown} className="select-control-chevron" />
    </span>
  );
}
