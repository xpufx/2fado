import { z } from "zod";
import { defineContract, defineSettingsContract } from "paseo-plugin-helper/shared";

export const pendingList = defineContract({
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
  description: "List unexpired, undecided 2fado requests",
});

export const verdict = defineContract({
  name: "approval.verdict",
  input: z.object({
    id: z.string(),
    decision: z.enum(["approve", "deny"]),
    socketPath: z.string().min(1).optional(),
  }),
  output: z.object({ recorded: z.boolean() }),
  description: "Record an approve/deny verdict for a 2fado request",
});

const settingsSchema = z.object({
  socketPath: z
    .string()
    .trim()
    .min(1, "Enter the 2fadod socket path")
    .default("/tmp/2fado.sock")
    .describe("2fadod socket"),
  telegramFallback: z.boolean().default(true).describe("Telegram fallback"),
});

export type ApprovalSettingsValues = z.output<typeof settingsSchema>;

export const approvalSettings = defineSettingsContract<ApprovalSettingsValues>({
  name: "twofado.settings",
  schema: settingsSchema,
  description: "2fado approval settings",
});
