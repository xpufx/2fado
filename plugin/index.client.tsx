import type { PluginClientContext } from "@getpaseo/plugin/client";
import { ApprovalPopover, ApprovalPillIcon, trackPill, untrackPill } from "./client/approvals";
import { ApprovalSettings } from "./client/settings";

export default function contribute(client: PluginClientContext) {
  const pills = new Map<string, () => void>();

  const unsubscribe = client.paseo.agents.subscribe((update) => {
    if (update.kind !== "upsert" || !update.agent.workspaceId) return;
    const { id: agentId, workspaceId } = update.agent;
    if (pills.has(agentId)) return;
    const registration = client.addComposerPill({
      id: "fado-approval",
      workspaceId,
      agentId,
      button: {
        title: "2fado approvals",
        label: "2fado",
        icon: ApprovalPillIcon,
        behavior: { kind: "popover", Content: ApprovalPopover },
      },
    });
    pills.set(agentId, () => {
      untrackPill(agentId);
      registration.remove();
    });
    trackPill(agentId, registration);
  });

  const removeSettings = client.addSettingsScreen({
    id: "fado-approval",
    title: "2fado approval",
    icon: "ShieldCheck",
    Component: ApprovalSettings,
  });

  return () => {
    unsubscribe();
    for (const remove of pills.values()) remove();
    pills.clear();
    removeSettings();
  };
}
