import * as React from "react";

import { canAtLeast, type ResourcePermission } from "@/lib/types";

/**
 * Decide whether a button should be disabled based on the caller's
 * permission on a resource, and the level that action requires.
 *
 * Returns `disabled: false` when the user has enough permission; otherwise
 * `disabled: true` with a `reason` string for the tooltip. Global admin
 * permissions are always "admin" in the API output, so the rank check
 * already covers them.
 */
export function permGate(
  currentPerm: ResourcePermission | string | undefined,
  required: "read" | "write" | "admin",
): { disabled: boolean; reason: string } {
  if (canAtLeast(currentPerm, required)) {
    return { disabled: false, reason: "" };
  }
  return {
    disabled: true,
    reason: `需要 ${required} 权限`,
  };
}

/**
 * React wrapper that drops in a `disabled` + `title` pair onto a button.
 * Usage:
 *   <Button {...disableIf(permGate(t.current_user_permission, "write"))}
 *           onClick={...}>
 */
export function disableIf(gate: { disabled: boolean; reason: string }) {
  return {
    disabled: gate.disabled,
    title: gate.disabled ? gate.reason : undefined,
  } as const;
}
