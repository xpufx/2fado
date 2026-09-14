import { SettingsSelect } from "@getpaseo/plugin/client/ui";
import type { HelperSettingsSelectProps } from "paseo-plugin-helper/client";
import { FormRow, Tabs } from "paseo-plugin-helper/client";
import { notificationTargets, type NotificationTarget } from "../shared/approval";

const SEGMENT_LABELS: Record<NotificationTarget, string> = {
  telegram: "Telegram",
  paseo: "Paseo",
  both: "Both",
};

function isTargetOptions(
  options: ReadonlyArray<{ label: string; value: string }>,
): options is ReadonlyArray<{ label: string; value: NotificationTarget }> {
  if (options.length !== notificationTargets.length) return false;
  const values = new Set(options.map((option) => option.value));
  return notificationTargets.every((target) => values.has(target));
}

// Helper SettingsSelect is dropdown-only, so the 3-way notification target
// renders as a fitted Tabs segmented switch; every other enum falls through.
export function NotificationTargetSelect<Value extends string = string>(
  props: HelperSettingsSelectProps<Value>,
) {
  const { label, hint, value, options, onValueChange } = props;
  if (!isTargetOptions(options)) {
    return <SettingsSelect {...props} />;
  }
  return (
    <FormRow label={label} description={hint}>
      <Tabs
        tabs={notificationTargets.map((target) => ({
          id: target,
          label: SEGMENT_LABELS[target],
        }))}
        activeTab={value}
        onTabChange={(id) => onValueChange(id as Value)}
        mode="fit"
      />
    </FormRow>
  );
}
