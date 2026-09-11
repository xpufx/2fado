import net from "node:net";
import os from "node:os";
import type { RpcInput, RpcOutput } from "@getpaseo/plugin";
import { pendingList, verdict } from "../shared/approval";

const DEFAULT_SOCKET = "/run/2fado.sock";
const LIVE_SOCKET = "/tmp/2fado-live.sock";

function socketCandidates(configured?: string): string[] {
  const candidates: string[] = [];
  const push = (sock: string | undefined) => {
    const trimmed = sock?.trim();
    if (trimmed !== undefined && trimmed.length > 0 && !candidates.includes(trimmed)) {
      candidates.push(trimmed);
    }
  };
  push(configured);
  push(process.env.FADO_SOCKET);
  push(DEFAULT_SOCKET);
  push(LIVE_SOCKET);
  return candidates;
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

function callDaemonOn(sock: string, message: unknown, timeoutMs: number): Promise<unknown> {
  return new Promise((resolve, reject) => {
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
    socket.on("close", () => fail(new Error(`2fadod closed connection (${sock})`)));
  });
}

async function callDaemon(
  message: unknown,
  configured?: string,
  timeoutMs = 2000,
): Promise<unknown> {
  let lastError: unknown = new Error("no 2fadod socket candidates");
  for (const sock of socketCandidates(configured)) {
    try {
      return await callDaemonOn(sock, message, timeoutMs);
    } catch (err) {
      lastError = err;
    }
  }
  throw lastError instanceof Error ? lastError : new Error(String(lastError));
}

export async function listPending(
  input: RpcInput<typeof pendingList>,
): Promise<RpcOutput<typeof pendingList>> {
  const raw = (await callDaemon({ list: {} }, input.socketPath)) as DaemonPendingList;
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
    const raw = (await callDaemon(
      {
        verdict: { id: input.id, decision: input.decision, by: "paseo" },
      },
      input.socketPath,
    )) as { recorded: boolean };
    return { recorded: raw.recorded === true };
  } catch {
    return { recorded: false };
  }
}
