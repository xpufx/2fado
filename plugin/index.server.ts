import type { PluginServerContext } from "@getpaseo/plugin/server";
import { pendingList, verdict, approvalSettings } from "./shared/approval";
import { listPending, submitVerdict } from "./server/fado";

export default function contribute(server: PluginServerContext) {
  server.registerSettings(approvalSettings);
  server.handle(pendingList, listPending);
  server.handle(verdict, submitVerdict);
  return () => {};
}
