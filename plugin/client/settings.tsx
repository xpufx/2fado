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
  const dirty = draftSocket !== values.socketPath;

  return (
    <SettingsSection title="2fado approval">
      <SettingsCard>
        <SettingsInput
          label="2fadod socket"
          hint="Daemon-local path. The app sends it with every list and verdict call; the server tries it first, then FADO_SOCKET and the built-in defaults."
          initialValue={draftSocket}
          onChangeText={setSocketPath}
          disabled={settings.saving}
          error={settings.saveError}
        />
        <SettingsSwitch
          label="Telegram fallback"
          hint="Keep paging over Telegram until the Paseo path proves itself."
          value={values.telegramFallback}
          disabled={settings.saving}
          onValueChange={(telegramFallback) =>
            void settings.save(
              { socketPath: draftSocket, telegramFallback },
              settings.revision,
            )
          }
        />
        <SettingsAction
          label="Socket path"
          actionLabel="Save"
          disabled={settings.saving || !dirty}
          onPress={() =>
            void settings
              .save({ ...values, socketPath: draftSocket }, settings.revision)
              .then((ok) => {
                if (ok) setSocketPath(null);
              })
          }
        />
      </SettingsCard>
      <Text style={hint}>
        Anyone holding this app can approve. Real gating belongs in 2fadod, not here.
      </Text>
    </SettingsSection>
  );
}
