import type { PluginClientContext, PluginSurfaceProps } from "@getpaseo/plugin/client";
import {
  ApprovalHeaderIcon,
  ApprovalSurface,
  trackHeaderButton,
  untrackHeaderButton,
} from "./client/approvals";
import { ApprovalSettings } from "./client/settings";

export default function contribute(client: PluginClientContext) {
  const Surface = (props: PluginSurfaceProps) => (
    <ApprovalSurface {...props} onOpenSettings={() => client.openSettings("fado-approval")} />
  );
  client.addSurface("approvals", Surface);
  client.addSidebarItem({
    id: "approvals",
    title: "2fado approvals",
    icon: "ShieldCheck",
    surface: "approvals",
  });
  client.addCommandCenterItem({
    id: "open-approvals",
    title: "Open 2fado approvals",
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
      openSettings("fado-approval");
    },
  });

  const buttons = new Map<string, () => void>();
  const unsubscribe = client.paseo.agents.subscribe((update) => {
    if (update.kind !== "upsert" || !update.agent.workspaceId) return;
    const { workspaceId } = update.agent;
    if (buttons.has(workspaceId)) return;
    const registration = client.addHeaderButton({
      id: "fado-approval",
      workspaceId,
      button: {
        title: "2fado approvals",
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

  const removeSettings = client.addSettingsScreen({
    id: "fado-approval",
    title: "2fado approval",
    icon: "ShieldCheck",
    Component: ApprovalSettings,
  });

  return () => {
    unsubscribe();
    for (const remove of buttons.values()) remove();
    buttons.clear();
    removeSettings();
  };
}
