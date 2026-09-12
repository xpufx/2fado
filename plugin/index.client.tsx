import type { PluginClientContext, PluginSurfaceProps } from "@getpaseo/plugin/client";
import { useRpc } from "@getpaseo/plugin/client";
import { Icon, Modal, ScrollView, useToast } from "@getpaseo/plugin/client/react-native";
import {
  SettingsCard,
  SettingsInput,
  SettingsSection,
  SettingsSelect,
  SettingsSwitch,
} from "@getpaseo/plugin/client/ui";
import { initClientHelpers, registerHelperSettingsScreen } from "paseo-plugin-helper/client";
import { approvalSettings } from "./shared/approval";
import {
  ApprovalHeaderIcon,
  ApprovalSurface,
  trackHeaderButton,
  untrackHeaderButton,
} from "./client/approvals";

export default function contribute(client: PluginClientContext) {
  initClientHelpers({ Icon, Modal, useRpc, useToast, ScrollView });

  const Surface = (props: PluginSurfaceProps) => (
    <ApprovalSurface {...props} onOpenSettings={() => client.openSettings("twofado")} />
  );
  client.addSurface("approvals", Surface);
  client.addSidebarItem({
    id: "approvals",
    title: "2fado",
    icon: "ShieldCheck",
    surface: "approvals",
  });
  client.addCommandCenterItem({
    id: "open-approvals",
    title: "Open 2fado",
    icon: "ShieldCheck",
    context: "global",
    onSelect({ openSurface }) {
      openSurface("approvals");
    },
  });
  client.addCommandCenterItem({
    id: "configure-approvals",
    title: "Configure 2fado approval",
    icon: "Settings",
    context: "global",
    onSelect({ openSettings }) {
      openSettings("twofado");
    },
  });

  const removeSettingsScreen = registerHelperSettingsScreen(client, approvalSettings, {
    id: "twofado",
    title: "2fado approval",
    icon: "ShieldCheck",
    ui: { SettingsCard, SettingsSection, SettingsSwitch, SettingsSelect, SettingsInput },
    labels: {
      socketPath: "2fadod socket",
      telegramFallback: "Telegram fallback",
      telegramBotToken: "Telegram bot token",
      telegramChatId: "Telegram chat ID",
      telegramApprovers: "Authorized approvers",
    },
    descriptions: {
      socketPath: "Daemon-local path. The app sends it with every call; the server tries it first.",
      telegramFallback: "Keep paging over Telegram until the Paseo path proves itself.",
      telegramBotToken: "Bot token from @BotFather (e.g. 123456:ABC-DEF...). Synced to daemon config.",
      telegramChatId: "Target Telegram chat or channel ID (e.g. -100123456789 or 12345678).",
      telegramApprovers: "Comma-separated Telegram usernames authorized to approve (e.g. @alice, @bob).",
    },
  });

  const buttons = new Map<string, () => void>();
  const unsubscribe = client.paseo.agents.subscribe((update) => {
    if (update.kind !== "upsert" || !update.agent.workspaceId) return;
    const { workspaceId } = update.agent;
    if (buttons.has(workspaceId)) return;
    const registration = client.addHeaderButton({
      id: "twofado",
      workspaceId,
      button: {
        title: "2fado",
        icon: ApprovalHeaderIcon,
        behavior: { kind: "action", onPress: () => client.openSurface("approvals") },
      },
    });
    buttons.set(workspaceId, () => {
      untrackHeaderButton(workspaceId);
      registration.remove();
    });
    trackHeaderButton(workspaceId, registration);
  });

  return () => {
    unsubscribe();
    for (const remove of buttons.values()) remove();
    buttons.clear();
    removeSettingsScreen();
  };
}
