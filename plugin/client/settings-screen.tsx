import { useRpc } from "@getpaseo/plugin/client";
import { useToast } from "@getpaseo/plugin/client/react-native";
import { SettingsCard, SettingsInput, SettingsSection } from "@getpaseo/plugin/client/ui";
import { useEffect, useRef, useState } from "react";
import { approvalSettings, notificationTargets, type ApprovalSettingsValues } from "../shared/approval";
import { NotificationTargetSelect } from "./notification-target-select";

const FALLBACK: ApprovalSettingsValues = {
  socketPath: "/tmp/2fado.sock",
  notificationTarget: "both",
  telegramBotToken: "",
  telegramChatId: "",
  telegramApprovers: "",
};

// Workaround for paseo-plugin-helper#settings-initialValue: the helper renders
// string fields as uncontrolled SettingsInput (initialValue only) with a stable
// key, so values arriving after the async get() never appear. Remounting each
// input once the stored values load forces initialValue to take effect.
export function TwofadoSettingsScreen() {
  const get = useRpc(approvalSettings.get);
  const update = useRpc(approvalSettings.update);
  const toast = useToast();
  const [draft, setDraft] = useState<ApprovalSettingsValues>(FALLBACK);
  const [loaded, setLoaded] = useState(false);
  const saveTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const mounted = useRef(true);

  useEffect(() => {
    mounted.current = true;
    get({})
      .then((res) => {
        if (!mounted.current) return;
        setDraft({ ...FALLBACK, ...res });
        setLoaded(true);
      })
      .catch(() => {
        if (!mounted.current) return;
        toast.error("Failed to load 2fado settings.");
      });
    return () => {
      mounted.current = false;
      if (saveTimer.current) clearTimeout(saveTimer.current);
    };
  }, [get, toast]);

  const patch = (p: Partial<ApprovalSettingsValues>) => {
    setDraft((prev) => ({ ...prev, ...p }));
    if (saveTimer.current) clearTimeout(saveTimer.current);
    saveTimer.current = setTimeout(() => {
      update(p).catch(() => {
        toast.error("Failed to save 2fado settings.");
      });
    }, 300);
  };

  const loadKey = loaded ? "ready" : "loading";

  return (
    <SettingsCard>
      <SettingsSection title="2fado approval">
        <SettingsInput
          key={`socketPath:${loadKey}`}
          label="2fadod socket"
          hint="Daemon-local path. The app sends it with every call; the server tries it first."
          initialValue={draft.socketPath}
          onChangeText={(text) => patch({ socketPath: text })}
        />
        <NotificationTargetSelect
          label="Notification target"
          hint="Where new requests page: Telegram, Paseo, or Both (default)."
          value={draft.notificationTarget}
          options={notificationTargets.map((target) => ({ label: target, value: target }))}
          onValueChange={(value) => patch({ notificationTarget: value })}
        />
        <SettingsInput
          key={`telegramBotToken:${loadKey}`}
          label="Telegram bot token"
          hint="Bot token from @BotFather (e.g. 123456:ABC-DEF...). Synced to daemon config."
          initialValue={draft.telegramBotToken}
          secureTextEntry
          onChangeText={(text) => patch({ telegramBotToken: text })}
        />
        <SettingsInput
          key={`telegramChatId:${loadKey}`}
          label="Telegram chat ID"
          hint="Target Telegram chat or channel ID (e.g. -100123456789 or 12345678)."
          initialValue={draft.telegramChatId}
          onChangeText={(text) => patch({ telegramChatId: text })}
        />
        <SettingsInput
          key={`telegramApprovers:${loadKey}`}
          label="Authorized approvers"
          hint="Comma-separated Telegram usernames authorized to approve (e.g. @alice, @bob)."
          initialValue={draft.telegramApprovers}
          onChangeText={(text) => patch({ telegramApprovers: text })}
        />
      </SettingsSection>
    </SettingsCard>
  );
}
