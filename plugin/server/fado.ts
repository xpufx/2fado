import net from "node:net";
import os from "node:os";
import type { RpcInput, RpcOutput } from "paseo-plugin-helper/shared";
import { createPluginLogger, guardRpcHandler } from "paseo-plugin-helper/server";
import { pendingList, recentList, verdict } from "../shared/approval";

const log = createPluginLogger("twofado", { banner: false });

const DEFAULT_SOCKET = "/tmp/2fado.sock";

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

interface DaemonRecentItem {
  id: string;
  argv: string[];
  cwd: string;
  decision: string;
  by: string;
  exit: number;
  output: string;
}

interface DaemonRecentList {
  items: DaemonRecentItem[];
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

async function listPendingInner(
  input?: RpcInput<typeof pendingList>,
): Promise<RpcOutput<typeof pendingList>> {
  const raw = (await callDaemon({ list: {} }, input?.socketPath)) as DaemonPendingList;
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

async function submitVerdictInner(
  input?: RpcInput<typeof verdict>,
): Promise<RpcOutput<typeof verdict>> {
  if (input === undefined) return { recorded: false };
  try {
    const raw = (await callDaemon(
      {
        verdict: { id: input.id, decision: input.decision, by: "paseo" },
      },
      input.socketPath,
    )) as { recorded: boolean };
    return { recorded: raw.recorded === true };
  } catch (err) {
    log.warn("verdict submit failed", { id: input.id, error: err });
    return { recorded: false };
  }
}

export const listPending = guardRpcHandler(listPendingInner, {
  timeoutMs: 5000,
  maxInflight: 4,
  onTimeout: (info) => log.warn("list timed out", info),
  onSaturated: (info) => log.warn("list saturated, shedding load", info),
});

async function listRecentInner(
  input?: RpcInput<typeof recentList>,
): Promise<RpcOutput<typeof recentList>> {
  const limit = input?.limit ?? 10;
  try {
    const raw = (await callDaemon({ recent: { limit } }, input?.socketPath)) as DaemonRecentList;
    const items = Array.isArray(raw.items) ? raw.items : [];
    return {
      items: items.map((item) => ({
        id: item.id,
        argv: Array.isArray(item.argv) ? item.argv : [],
        cwd: typeof item.cwd === "string" ? item.cwd : "",
        decision: typeof item.decision === "string" ? item.decision : "",
        by: typeof item.by === "string" ? item.by : "",
        exit: typeof item.exit === "number" ? item.exit : -1,
        output: typeof item.output === "string" ? item.output : "",
      })),
    };
  } catch (err) {
    log.warn("recent list failed", { error: err });
    return { items: [] };
  }
}

export const listRecent = guardRpcHandler(listRecentInner, {
  timeoutMs: 5000,
  maxInflight: 4,
  onTimeout: (info) => log.warn("recent timed out", info),
  onSaturated: (info) => log.warn("recent saturated, shedding load", info),
});

export const submitVerdict = guardRpcHandler(submitVerdictInner, {
  timeoutMs: 5000,
  maxInflight: 4,
  onTimeout: (info) => log.warn("verdict timed out", info),
  onSaturated: (info) => log.warn("verdict saturated, shedding load", info),
});
