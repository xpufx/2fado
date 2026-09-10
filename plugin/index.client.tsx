import type { PluginClientContext } from "@getpaseo/plugin/client";
import { GreetingSurface } from "./client/greeting";

export default function contribute(client: PluginClientContext) {
  client.addSurface("greeting", GreetingSurface);
  client.addSidebarItem({
    id: "greeting",
    title: "Greeting",
    icon: "MessageCircle",
    surface: "greeting",
  });
  return () => {};
}
