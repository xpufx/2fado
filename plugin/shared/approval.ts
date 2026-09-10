import { defineRpc, defineSettings } from "@getpaseo/plugin";
import { z } from "zod";

export const pendingList = defineRpc({
  name: "approval.list",
  input: z.object({}),
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
  }),
  output: z.object({ recorded: z.boolean() }),
});

export const approvalSettings = defineSettings({
  id: "fado-approval",
  scope: "host",
  version: 1,
  schema: z.object({
    socketPath: z.string().trim().min(1, "Enter the 2fadod socket path").default("/run/2fado.sock"),
    approvers: z
      .string()
      .trim()
      .default("")
      .describe("Comma-separated Paseo user ids. Empty disables all approvals."),
    telegramFallback: z.boolean().default(true),
  }),
});
