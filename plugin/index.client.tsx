import type { PluginClientContext, PluginSurfaceProps } from "@getpaseo/plugin/client";
import { useRpc } from "@getpaseo/plugin/client";
import { Icon, Modal, ScrollView, useToast } from "@getpaseo/plugin/client/react-native";
import { initClientHelpers } from "paseo-plugin-helper/client";
import { TwofadoSettingsScreen } from "./client/settings-screen";
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

  // Local settings screen (not registerHelperSettingsScreen): the helper renders
  // string fields as uncontrolled SettingsInput with a stable key, so stored
  // values arriving after the async get() never populate the inputs.
  const removeSettingsScreen = client.addSettingsScreen({
    id: "twofado",
    title: "2fado approval",
    icon: "ShieldCheck",
    Component: TwofadoSettingsScreen,
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
