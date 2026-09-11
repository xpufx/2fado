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
        step: z.enum(["initial", "confirm"]).default("initial"),
        confirmOf: z.string().optional(),
        preview: z
          .object({
            resolvedBinary: z.string().optional(),
            targetCwd: z.string().optional(),
            affectedCount: z.number().optional(),
            samplePaths: z.array(z.string()).optional(),
            riskLevel: z.enum(["low", "medium", "high", "critical"]).optional(),
            riskReason: z.string().optional(),
          })
          .optional(),
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

export const recentList = defineContract({
  name: "approval.recent",
  input: z.object({
    socketPath: z.string().min(1).optional(),
    limit: z.number().int().min(1).max(50).optional(),
  }),
  output: z.object({
    items: z.array(
      z.object({
        id: z.string(),
        argv: z.array(z.string()),
        cwd: z.string(),
        decision: z.string(),
        by: z.string(),
        exit: z.number(),
        output: z.string(),
      }),
    ),
  }),
  description: "List recently decided 2fado requests with execution results",
});

export const approvalStatus = defineContract({
  name: "approval.status",
  input: z.object({
    id: z.string().min(1),
    socketPath: z.string().min(1).optional(),
  }),
  output: z.object({
    id: z.string(),
    status: z.enum([
      "not_found",
      "pending",
      "confirming",
      "running",
      "completed",
      "denied",
      "timeout",
      "client_aborted",
      "confirmation_timeout",
    ]),
    argv: z.array(z.string()).optional(),
    cwd: z.string().optional(),
    expiresIn: z.number().optional(),
    decision: z.string().optional(),
    by: z.string().optional(),
    exit: z.number(),
    output: z.string().optional(),
    step: z.string().optional(),
    confirmOf: z.string().optional(),
  }),
  description: "Query status and execution outcome of a 2fado request by ID",
});

export const approvalTelegramInfo = defineContract({
  name: "approval.telegram_info",
  input: z.object({
    socketPath: z.string().min(1).optional(),
  }),
  output: z.object({
    configured: z.boolean(),
    botUsername: z.string().optional(),
    chatId: z.string().optional(),
    approvers: z.array(z.string()).default([]),
    status: z.enum(["connected", "disconnected", "unconfigured", "error"]).default("unconfigured"),
    error: z.string().optional(),
  }),
  description: "Query Telegram bot connectivity status and recipient metadata",
});

export const approvalTelegramSetConfig = defineContract({
  name: "approval.telegram_set_config",
  input: z.object({
    botToken: z.string().optional(),
    chatId: z.string().optional(),
    approvers: z.array(z.string()).optional(),
    socketPath: z.string().min(1).optional(),
  }),
  output: z.object({
    success: z.boolean(),
    botUsername: z.string().optional(),
    error: z.string().optional(),
  }),
  description: "Send updated Telegram recipient credentials to 2fadod",
});

const settingsSchema = z.object({
  socketPath: z
    .string()
    .trim()
    .min(1, "Enter the 2fadod socket path")
    .default("/tmp/2fado.sock")
    .describe("2fadod socket"),
  telegramFallback: z.boolean().default(true).describe("Telegram fallback"),
  telegramBotToken: z.string().default("").describe("Telegram bot token"),
  telegramChatId: z.string().default("").describe("Telegram chat ID"),
  telegramApprovers: z.string().default("").describe("Telegram approvers"),
});

export type ApprovalSettingsValues = z.output<typeof settingsSchema>;

export const approvalSettings = defineSettingsContract<ApprovalSettingsValues>({
  name: "twofado.settings",
  schema: settingsSchema,
  description: "2fado approval settings",
});
