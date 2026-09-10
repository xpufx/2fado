import type { PluginSurfaceProps } from "@getpaseo/plugin/client";
import { useSettings } from "@getpaseo/plugin/client";
import {
  SettingsAction,
  SettingsCard,
  SettingsInput,
  SettingsSection,
  SettingsSwitch,
} from "@getpaseo/plugin/client/ui";
import { useMemo, useState } from "react";
import { Text } from "react-native";
import { approvalSettings } from "../shared/approval";

export function ApprovalSettings({ theme }: PluginSurfaceProps) {
  const settings = useSettings(approvalSettings);
  const [socketPath, setSocketPath] = useState<string | null>(null);
  const [approvers, setApprovers] = useState<string | null>(null);
  const style = useMemo(() => ({ color: theme.colors.foreground }), [theme]);
  const hint = useMemo(() => ({ color: theme.colors.foregroundMuted, fontSize: 12 }), [theme]);

  if (settings.status === "loading") return <Text style={style}>Loading settings…</Text>;
  if (settings.status !== "ready")
    return (
      <SettingsSection title="2fado approval">
        <Text style={style}>{settings.error}</Text>
        <SettingsAction label="Try again" actionLabel="Reload" onPress={settings.reload} />
      </SettingsSection>
    );

  const values = settings.values;
  const draftSocket = socketPath ?? values.socketPath;
  const draftApprovers = approvers ?? values.approvers;
  const dirty = draftSocket !== values.socketPath || draftApprovers !== values.approvers;

  return (
    <SettingsSection title="2fado approval">
      <SettingsCard>
        <SettingsInput
          label="2fadod socket"
          hint="Daemon-local path. The server also honors FADO_SOCKET (default /run/2fado.sock)."
          initialValue={draftSocket}
          onChangeText={setSocketPath}
          disabled={settings.saving}
          error={settings.saveError}
        />
        <SettingsInput
          label="Approvers"
          hint="Comma-separated Paseo user ids. Empty disables all approvals (fail closed)."
          initialValue={draftApprovers}
          onChangeText={setApprovers}
          placeholder="octavia, bruno"
          disabled={settings.saving}
          error={settings.saveError}
        />
        <SettingsSwitch
          label="Telegram fallback"
          hint="Keep paging over Telegram until the Paseo path proves itself."
          value={values.telegramFallback}
          disabled={settings.saving}
          onValueChange={(telegramFallback) =>
            void settings.save({ ...values, socketPath: draftSocket, approvers: draftApprovers, telegramFallback }, settings.revision)
          }
        />
        <SettingsAction
          label="Socket and approvers"
          actionLabel="Save"
          disabled={settings.saving || !dirty}
          onPress={() =>
            void settings
              .save(
                { ...values, socketPath: draftSocket, approvers: draftApprovers },
                settings.revision,
              )
              .then((ok) => {
                if (ok) {
                  setSocketPath(null);
                  setApprovers(null);
                }
              })
          }
        />
      </SettingsCard>
      <Text style={hint}>
        Approval buttons stay disabled until at least one approver is configured.
      </Text>
    </SettingsSection>
  );
}
