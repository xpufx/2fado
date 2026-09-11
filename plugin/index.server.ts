import type { PluginServerContext } from "@getpaseo/plugin/server";
import { PluginStorage, createPluginLogger, registerSettingsRpc } from "paseo-plugin-helper/server";
import { approvalSettings, pendingList, verdict } from "./shared/approval";
import { listPending, submitVerdict } from "./server/fado";

const log = createPluginLogger("fado-approval");

export default function contribute(server: PluginServerContext) {
  const storage = new PluginStorage("fado-approval", "settings.json", {
    defaultData: approvalSettings.defaultSettings,
    schema: approvalSettings.schema,
  });
  registerSettingsRpc(server, approvalSettings, storage, {
    onUpdate: (next) => log.info("settings updated", { socketPath: next.socketPath }),
  });
  server.handle(pendingList, (input) => listPending(input));
  server.handle(verdict, (input) => submitVerdict(input));
  return () => {};
}
