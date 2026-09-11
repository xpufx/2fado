import { defineRpc, defineSettings } from "@getpaseo/plugin";
import { z } from "zod";

export const pendingList = defineRpc({
  name: "approval.list",
  input: z.object({ socketPath: z.string().min(1).optional() }),
  output: z.object({
    items: z.array(
      z.object({
        id: z.string(),
        argv: z.array(z.string()),
        host: z.string(),
        caller: z.string(),
        cwd: z.string(),
        expiresIn: z.number(),
      }),
    ),
  }),
});

export const verdict = defineRpc({
  name: "approval.verdict",
  input: z.object({
    id: z.string(),
    decision: z.enum(["approve", "deny"]),
    socketPath: z.string().min(1).optional(),
  }),
  output: z.object({ recorded: z.boolean() }),
});

export const approvalSettings = defineSettings({
  id: "fado-approval",
  scope: "host",
  version: 2,
  schema: z.object({
    socketPath: z.string().trim().min(1, "Enter the 2fadod socket path").default("/run/2fado.sock"),
    telegramFallback: z.boolean().default(true),
  }),
  migrate: (values) => {
    if (typeof values !== "object" || values === null) return values;
    const v = values as Record<string, unknown>;
    return { socketPath: v["socketPath"], telegramFallback: v["telegramFallback"] };
  },
});
