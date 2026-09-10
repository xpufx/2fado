import net from "node:net";
import os from "node:os";
import type { RpcInput, RpcOutput } from "@getpaseo/plugin";
import { pendingList, verdict } from "../shared/approval";

const DEFAULT_SOCKET = "/run/2fado.sock";

function socketPath(): string {
  const env = process.env.FADO_SOCKET?.trim();
  return env !== undefined && env.length > 0 ? env : DEFAULT_SOCKET;
}

interface DaemonPendingItem {
  id: string;
  argv: string[];
  uid: number;
  cwd: string;
  expires_in: number;
}

interface DaemonPendingList {
  items: DaemonPendingItem[];
}

function callDaemon(message: unknown, timeoutMs = 5000): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const sock = socketPath();
    let buffer = "";
    let settled = false;
    const socket = net.createConnection(sock);
    const fail = (err: Error) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      socket.destroy();
      reject(err);
    };
    const timer = setTimeout(() => fail(new Error(`2fadod timeout (${sock})`)), timeoutMs);
    socket.setEncoding("utf8");
    socket.on("connect", () => {
      socket.write(JSON.stringify(message) + "\n");
    });
    socket.on("data", (data: string) => {
      buffer += data;
      const nl = buffer.indexOf("\n");
      if (nl >= 0) {
        const line = buffer.slice(0, nl);
        if (settled) return;
        settled = true;
        clearTimeout(timer);
        socket.destroy();
        try {
          resolve(JSON.parse(line));
        } catch (err) {
          reject(err instanceof Error ? err : new Error(String(err)));
        }
      }
    });
    socket.on("error", (err) => fail(err instanceof Error ? err : new Error(String(err))));
  });
}

export async function listPending(): Promise<RpcOutput<typeof pendingList>> {
  const raw = (await callDaemon({ list: {} })) as DaemonPendingList;
  const host = os.hostname();
  const items = Array.isArray(raw.items) ? raw.items : [];
  return {
    items: items.map((item) => ({
      id: item.id,
      argv: item.argv,
      host,
      caller: String(item.uid),
      cwd: item.cwd,
      expiresIn: item.expires_in,
    })),
  };
}

export async function submitVerdict(
  input: RpcInput<typeof verdict>,
): Promise<RpcOutput<typeof verdict>> {
  try {
    const raw = (await callDaemon({
      verdict: { id: input.id, decision: input.decision, by: "paseo" },
    })) as { recorded: boolean };
    return { recorded: raw.recorded === true };
  } catch {
    return { recorded: false };
  }
}
