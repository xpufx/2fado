import type { PluginServerContext } from "@getpaseo/plugin/server";
import { PluginStorage, createPluginLogger, registerSettingsRpc } from "paseo-plugin-helper/server";
import {
  approvalSettings,
  approvalStatus,
  approvalTelegramInfo,
  approvalTelegramSetConfig,
  pendingList,
  policyAddRule,
  recentList,
  verdict,
} from "./shared/approval";
import {
  addPolicyRule,
  getStatus,
  getTelegramInfo,
  listPending,
  listRecent,
  setTelegramConfig,
  submitVerdict,
} from "./server/fado";

const log = createPluginLogger("twofado");

export default function contribute(server: PluginServerContext) {
  const storage = new PluginStorage("twofado", "settings.json", {
    defaultData: approvalSettings.defaultSettings,
    schema: approvalSettings.schema,
  });
  registerSettingsRpc(server, approvalSettings, storage, {
    onUpdate: (next) => {
      log.info("settings updated", { socketPath: next.socketPath });
      if (next.telegramBotToken || next.telegramChatId || next.telegramApprovers) {
        const approvers = next.telegramApprovers
          ? next.telegramApprovers
              .split(/[\s,]+/)
              .map((s) => s.trim())
              .filter(Boolean)
          : [];
        void setTelegramConfig({
          botToken: next.telegramBotToken || undefined,
          chatId: next.telegramChatId || undefined,
          approvers,
          socketPath: next.socketPath,
        }).catch((err) => log.warn("failed to sync telegram config to daemon", { error: err }));
      }
    },
  });
  server.handle(pendingList, (input) => listPending(input));
  server.handle(verdict, (input) => submitVerdict(input));
  server.handle(recentList, (input) => listRecent(input));
  server.handle(approvalStatus, (input) => getStatus(input));
  server.handle(approvalTelegramInfo, (input) => getTelegramInfo(input));
  server.handle(approvalTelegramSetConfig, (input) => setTelegramConfig(input));
  server.handle(policyAddRule, (input) => addPolicyRule(input));
  return () => {};
}
