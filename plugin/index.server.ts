import type { PluginServerContext } from "@getpaseo/plugin/server";
import { PluginStorage, createPluginLogger, registerSettingsRpc } from "paseo-plugin-helper/server";
import { approvalSettings, pendingList, recentList, verdict } from "./shared/approval";
import { listPending, listRecent, submitVerdict } from "./server/fado";

const log = createPluginLogger("twofado");

export default function contribute(server: PluginServerContext) {
  const storage = new PluginStorage("twofado", "settings.json", {
    defaultData: approvalSettings.defaultSettings,
    schema: approvalSettings.schema,
  });
  registerSettingsRpc(server, approvalSettings, storage, {
    onUpdate: (next) => log.info("settings updated", { socketPath: next.socketPath }),
  });
  server.handle(pendingList, (input) => listPending(input));
  server.handle(verdict, (input) => submitVerdict(input));
  server.handle(recentList, (input) => listRecent(input));
  return () => {};
}
