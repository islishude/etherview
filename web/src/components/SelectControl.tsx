import "./SelectControl.css";
import type { ComponentPropsWithRef } from "react";
import { DropdownChevron } from "./DropdownChevron";

/** Keep native selection, labels, keyboard behavior and form submission. */
export function SelectControl(props: ComponentPropsWithRef<"select">) {
  if (props.multiple || props.size) return <select {...props} />;
  return (
    <span className="select-control">
      <select {...props} />
      <DropdownChevron className="select-control-chevron" />
    </span>
  );
}
