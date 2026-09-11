import { useRpc } from "@getpaseo/plugin/client";
import type {
  PluginButtonIconProps,
  PluginButtonRegistration,
  PluginSurfaceProps,
} from "@getpaseo/plugin/client";
import { Icon, ScrollView, useToast } from "@getpaseo/plugin/client/react-native";
import {
  Badge,
  Button,
  Card,
  CodeBlock,
  Collapsible,
  EmptyState,
  PluginThemeProvider,
  copyToClipboard,
  usePluginSettings,
  usePluginTheme,
} from "paseo-plugin-helper/client";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { Platform, Pressable, Text, View } from "react-native";
import {
  approvalSettings,
  approvalStatus,
  approvalTelegramInfo,
  pendingList,
  recentList,
  verdict,
} from "../shared/approval";

const LIST_KEY = ["twofado", "pending"];
const RECENT_KEY = ["twofado", "recent"];
const POLL_MS = 3000;
const RECENT_POLL_MS = 5000;
const RECENT_LIMIT = 10;
const OUTPUT_PREVIEW = 2000;
const EXPIRY_URGENT_S = 30;

const seenIds = new Set<string>();
const SEEN_IDS_CAP = 500;

function useNewPendingToast(
  items: Array<{ id: string; step?: "initial" | "confirm" }> | undefined,
) {
  const toast = useToast();
  useEffect(() => {
    const fresh = (items ?? []).filter((item) => !seenIds.has(item.id));
    if (fresh.length === 0) return;
    for (const item of fresh) seenIds.add(item.id);
    while (seenIds.size > SEEN_IDS_CAP) {
      const oldest = seenIds.values().next();
      if (oldest.done) break;
      seenIds.delete(oldest.value);
    }
    const hasConfirm = fresh.some((item) => item.step === "confirm");
    if (hasConfirm) {
      toast.show("⚠️ Are you sure? 2fado confirmation required", { variant: "warning" });
    } else {
      toast.show(
        fresh.length === 1 ? "2fado approval needed" : `${fresh.length} 2fado approvals needed`,
        { variant: "warning" },
      );
    }
  }, [items, toast]);
}
const headerRegistry = new Map<string, PluginButtonRegistration>();

export function trackHeaderButton(workspaceId: string, registration: PluginButtonRegistration) {
  headerRegistry.get(workspaceId)?.remove();
  headerRegistry.set(workspaceId, registration);
}

export function untrackHeaderButton(workspaceId: string) {
  headerRegistry.get(workspaceId)?.remove();
  headerRegistry.delete(workspaceId);
}

export function useSocketPath(): string | undefined {
  const { settings } = usePluginSettings(approvalSettings);
  const trimmed = settings.socketPath.trim();
  return trimmed.length > 0 ? trimmed : undefined;
}

export function usePendingList() {
  const list = useRpc(pendingList);
  const socketPath = useSocketPath();
  return useQuery({
    queryKey: [...LIST_KEY, socketPath ?? ""],
    queryFn: () => list({ socketPath }),
    refetchInterval: POLL_MS,
    retry: false,
  });
}

export function useRecentList() {
  const recent = useRpc(recentList);
  const socketPath = useSocketPath();
  return useQuery({
    queryKey: [...RECENT_KEY, socketPath ?? ""],
    queryFn: () => recent({ socketPath, limit: RECENT_LIMIT }),
    refetchInterval: RECENT_POLL_MS,
    retry: false,
  });
}

const TELEGRAM_KEY = ["twofado", "telegram"];

export function useTelegramInfo() {
  const getTelegram = useRpc(approvalTelegramInfo);
  const socketPath = useSocketPath();
  return useQuery({
    queryKey: [...TELEGRAM_KEY, socketPath ?? ""],
    queryFn: () => getTelegram({ socketPath }),
    refetchInterval: 15_000,
    retry: false,
  });
}

export function ApprovalHeaderIcon(props: PluginButtonIconProps) {
  const { theme } = props;
  const workspaceId = props.workspaceId;
  const size = props.size;
  const color = props.color;
  const { data } = usePendingList();
  const count = data?.items.length ?? 0;

  useNewPendingToast(data?.items);

  useEffect(() => {
    headerRegistry
      .get(workspaceId)
      ?.update({ label: count > 0 ? `${count} pending` : undefined });
  }, [workspaceId, count]);

  if (count === 0) return <Icon name="ShieldCheck" size={size} color={color} />;
  return (
    <PluginThemeProvider theme={{ colors: theme.colors }}>
      <Badge label={String(count)} variant="warning" icon="ShieldCheck" />
    </PluginThemeProvider>
  );
}

function commandLine(argv: string[]): string {
  return argv.map((arg) => (arg.includes(" ") ? JSON.stringify(arg) : arg)).join(" ");
}

function CommandBox({ argv }: { argv: string[] }) {
  const { colors } = usePluginTheme();
  const toast = useToast();
  const [copied, setCopied] = useState(false);
  const code = commandLine(argv);
  const [prog, ...rest] = argv;

  const handleCopy = async () => {
    const ok = await copyToClipboard(code, { toast, toastMessage: "Command" });
    if (ok) {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    }
  };

  const fontFamily = Platform.select({ ios: "Menlo", android: "monospace", default: "monospace" });

  return (
    <View
      style={{
        flexDirection: "row",
        alignItems: "center",
        justifyContent: "space-between",
        backgroundColor: colors.surface2,
        borderColor: colors.border,
        borderWidth: 1,
        borderRadius: 6,
        paddingHorizontal: 10,
        paddingVertical: 6,
        gap: 8,
      }}
    >
      <View style={{ flexDirection: "row", alignItems: "center", flex: 1, overflow: "hidden", gap: 6 }}>
        <Text style={{ color: colors.statusWarning, fontWeight: "700", fontFamily, fontSize: 12 }}>
          $
        </Text>
        <Text
          selectable
          numberOfLines={2}
          style={{ color: colors.foreground, fontFamily, fontSize: 12, flex: 1 }}
        >
          <Text style={{ fontWeight: "700", color: colors.foreground }}>{prog ?? ""}</Text>
          {rest.length > 0 ? (
            <Text style={{ color: colors.foregroundMuted }}>
              {" " + rest.map((a) => (a.includes(" ") ? JSON.stringify(a) : a)).join(" ")}
            </Text>
          ) : null}
        </Text>
      </View>
      <Pressable
        onPress={handleCopy}
        accessibilityRole="button"
        accessibilityLabel="Copy command"
        hitSlop={8}
        style={({ pressed }) => ({
          padding: 3,
          borderRadius: 4,
          backgroundColor: pressed ? colors.surface1 : "transparent",
        })}
      >
        <Icon
          name={copied ? "Check" : "Copy"}
          size={13}
          color={copied ? colors.statusSuccess : colors.foregroundMuted}
        />
      </Pressable>
    </View>
  );
}

function SectionHeader({ title, count }: { title: string; count?: number }) {
  const { colors } = usePluginTheme();
  return (
    <View style={{ flexDirection: "row", alignItems: "center", gap: 8, marginTop: 8, marginBottom: 2 }}>
      <Text
        style={{
          color: colors.foregroundMuted,
          fontSize: 11,
          fontWeight: "700",
          textTransform: "uppercase",
          letterSpacing: 0.8,
        }}
      >
        {title}
      </Text>
      {count !== undefined ? (
        <Badge
          label={String(count)}
          variant={count > 0 ? "warning" : "neutral"}
          styleVariant={count > 0 ? "solid" : "tinted"}
        />
      ) : null}
    </View>
  );
}

function ApprovalItem({
  item,
  deciding,
  onDecide,
}: {
  item: {
    id: string;
    argv: string[];
    host: string;
    caller: string;
    cwd: string;
    expiresIn: number;
    step?: "initial" | "confirm";
    confirmOf?: string;
    preview?: {
      resolvedBinary?: string;
      targetCwd?: string;
      affectedCount?: number;
      samplePaths?: string[];
      riskLevel?: "low" | "medium" | "high" | "critical";
      riskReason?: string;
    };
  };
  deciding?: "approve" | "deny";
  onDecide(item: { id: string; argv: string[]; cwd: string }, decision: "approve" | "deny"): void;
}) {
  const { colors } = usePluginTheme();
  const [program] = item.argv;
  const isConfirm = item.step === "confirm";
  const urgent = item.expiresIn <= EXPIRY_URGENT_S;
  const fontFamily = Platform.select({ ios: "Menlo", android: "monospace", default: "monospace" });
  const isDeciding = Boolean(deciding);

  return (
    <Card
      variant="elevated"
      style={{
        borderLeftWidth: 4,
        borderLeftColor: isConfirm || urgent ? colors.statusDanger : colors.statusWarning,
        gap: 8,
        padding: 12,
      }}
    >
      {isConfirm ? (
        <View
          style={{
            flexDirection: "row",
            alignItems: "center",
            gap: 8,
            backgroundColor: colors.statusDanger + "15",
            borderColor: colors.statusDanger + "40",
            borderWidth: 1,
            borderRadius: 6,
            paddingVertical: 6,
            paddingHorizontal: 10,
          }}
        >
          <Icon name="AlertTriangle" size={14} color={colors.statusDanger} />
          <View style={{ flex: 1 }}>
            <Text style={{ color: colors.statusDanger, fontSize: 12, fontWeight: "700" }}>
              ARE YOU SURE? Tap again to run
            </Text>
            {item.confirmOf ? (
              <Text style={{ color: colors.foregroundMuted, fontSize: 10 }}>
                Second confirmation for #{item.confirmOf.slice(0, 8)}
              </Text>
            ) : null}
          </View>
          <Badge label="2-step" variant="danger" styleVariant="solid" />
        </View>
      ) : null}

      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: 8 }}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flex: 1 }}>
          <View
            style={{
              width: 22,
              height: 22,
              borderRadius: 4,
              backgroundColor: isConfirm || urgent ? colors.statusDanger : colors.statusWarning,
              alignItems: "center",
              justifyContent: "center",
            }}
          >
            <Icon name={isConfirm ? "AlertTriangle" : "Terminal"} size={12} color="#ffffff" />
          </View>
          <Text style={{ color: colors.foreground, fontSize: 14, fontWeight: "700", flex: 1 }}>
            {program ?? "(empty)"}
          </Text>
        </View>
        <Badge
          label={urgent ? `expires in ${item.expiresIn}s` : `${item.expiresIn}s left`}
          variant={urgent || isConfirm ? "danger" : "warning"}
          styleVariant="solid"
          icon="Timer"
        />
      </View>

      <CommandBox argv={item.argv} />

      <View
        style={{
          flexDirection: "row",
          alignItems: "center",
          justifyContent: "space-between",
          flexWrap: "wrap",
          gap: 6,
        }}
      >
        <View style={{ flexDirection: "row", alignItems: "center", gap: 5 }}>
          <Icon name="User" size={11} color={colors.foregroundMuted} />
          <Text style={{ color: colors.foregroundMuted, fontSize: 11 }}>
            Caller <Text style={{ color: colors.foreground, fontWeight: "600" }}>{item.caller}</Text> on{" "}
            <Text style={{ color: colors.foreground, fontWeight: "600" }}>{item.host}</Text>
          </Text>
        </View>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 4, maxWidth: 280 }}>
          <Icon name="Folder" size={11} color={colors.foregroundMuted} />
          <Text
            numberOfLines={1}
            ellipsizeMode="middle"
            style={{ color: colors.foregroundMuted, fontSize: 11, fontFamily }}
          >
            {item.cwd}
          </Text>
        </View>
      </View>

      <Collapsible
        title={
          item.preview?.riskLevel === "critical" || item.preview?.riskLevel === "high"
            ? `Impact Preview · ${item.preview.riskLevel.toUpperCase()} RISK`
            : "Impact Preview"
        }
        icon={
          item.preview?.riskLevel === "critical" || item.preview?.riskLevel === "high"
            ? "AlertTriangle"
            : "Terminal"
        }
        initiallyExpanded={item.preview?.riskLevel === "critical" || item.preview?.riskLevel === "high"}
      >
        <View style={{ gap: 6, paddingTop: 4 }}>
          {item.preview?.riskReason ? (
            <View
              style={{
                backgroundColor: colors.statusDanger + "15",
                borderColor: colors.statusDanger + "40",
                borderWidth: 1,
                borderRadius: 6,
                padding: 8,
                flexDirection: "row",
                alignItems: "center",
                gap: 6,
              }}
            >
              <Icon name="AlertTriangle" size={13} color={colors.statusDanger} />
              <Text style={{ color: colors.statusDanger, fontSize: 11, fontWeight: "600", flex: 1 }}>
                {item.preview.riskReason}
              </Text>
            </View>
          ) : null}

          <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
            <Text style={{ color: colors.foregroundMuted, fontSize: 11 }}>Resolved Binary</Text>
            <Text style={{ color: colors.foreground, fontSize: 11, fontFamily, fontWeight: "600" }}>
              {item.preview?.resolvedBinary ?? `/usr/bin/${program ?? "command"}`}
            </Text>
          </View>

          <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
            <Text style={{ color: colors.foregroundMuted, fontSize: 11 }}>Target Directory</Text>
            <Text
              numberOfLines={1}
              ellipsizeMode="middle"
              style={{ color: colors.foreground, fontSize: 11, fontFamily, maxWidth: 220 }}
            >
              {item.preview?.targetCwd ?? item.cwd}
            </Text>
          </View>

          {item.preview?.affectedCount !== undefined ? (
            <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between" }}>
              <Text style={{ color: colors.foregroundMuted, fontSize: 11 }}>Affected Files</Text>
              <Badge
                label={`${item.preview.affectedCount} target${item.preview.affectedCount === 1 ? "" : "s"}`}
                variant={item.preview.affectedCount > 10 ? "warning" : "neutral"}
                styleVariant="tinted"
              />
            </View>
          ) : null}

          {item.preview?.samplePaths && item.preview.samplePaths.length > 0 ? (
            <View style={{ gap: 2, marginTop: 4 }}>
              <Text style={{ color: colors.foregroundMuted, fontSize: 10, fontWeight: "600" }}>
                Target Samples:
              </Text>
              {item.preview.samplePaths.slice(0, 3).map((p, idx) => (
                <Text
                  key={idx}
                  numberOfLines={1}
                  ellipsizeMode="middle"
                  style={{ color: colors.foregroundMuted, fontSize: 10, fontFamily }}
                >
                  • {p}
                </Text>
              ))}
            </View>
          ) : null}
        </View>
      </Collapsible>

      <View style={{ flexDirection: "row", gap: 8, paddingTop: 2 }}>
        <View style={{ flex: 1 }}>
          <Button
            label={isConfirm ? "Confirm & Run" : "Approve"}
            variant={isConfirm ? "danger" : "primary"}
            size="sm"
            icon={isConfirm ? "AlertTriangle" : "Check"}
            accessibilityLabel={`${isConfirm ? "Confirm" : "Approve"} ${item.id}`}
            loading={deciding === "approve"}
            disabled={isDeciding}
            onPress={() => onDecide(item, "approve")}
          />
        </View>
        <View style={{ flex: 1 }}>
          <Button
            label={isConfirm ? "Cancel & Deny" : "Deny"}
            variant={isConfirm ? "secondary" : "danger"}
            size="sm"
            icon="X"
            accessibilityLabel={`Deny ${item.id}`}
            loading={deciding === "deny"}
            disabled={isDeciding}
            onPress={() => onDecide(item, "deny")}
          />
        </View>
      </View>
    </Card>
  );
}

function ExecutingItem({
  item,
}: {
  item: {
    id: string;
    argv?: string[];
    cwd?: string;
    status: string;
    exit?: number;
    output?: string;
  };
}) {
  const { colors } = usePluginTheme();
  const [program] = item.argv ?? ["(command)"];
  const isConfirming = item.status === "confirming";
  const isCompleted = item.status === "completed";
  const isFailed = isCompleted && item.exit !== 0;
  const isSuccess = isCompleted && item.exit === 0;

  const statusColor = isSuccess
    ? colors.statusSuccess
    : isFailed || isConfirming
      ? colors.statusWarning
      : colors.accent;

  const fontFamily = Platform.select({ ios: "Menlo", android: "monospace", default: "monospace" });

  return (
    <Card
      variant="elevated"
      style={{
        borderLeftWidth: 4,
        borderLeftColor: statusColor,
        gap: 8,
        padding: 12,
      }}
    >
      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: 8 }}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 8, flex: 1 }}>
          <View
            style={{
              width: 22,
              height: 22,
              borderRadius: 4,
              backgroundColor: statusColor,
              alignItems: "center",
              justifyContent: "center",
            }}
          >
            <Icon
              name={isSuccess ? "Check" : isFailed || isConfirming ? "AlertTriangle" : "Activity"}
              size={12}
              color="#ffffff"
            />
          </View>
          <Text style={{ color: colors.foreground, fontSize: 14, fontWeight: "700", flex: 1 }}>
            {program}
          </Text>
        </View>
        <Badge
          label={
            isSuccess
              ? `exit ${item.exit}`
              : isFailed
                ? `exit ${item.exit}`
                : isConfirming
                  ? "confirming…"
                  : "running…"
          }
          variant={isSuccess ? "success" : isFailed || isConfirming ? "warning" : "accent"}
          styleVariant="solid"
          icon={isSuccess ? "Check" : isFailed || isConfirming ? "AlertTriangle" : "Activity"}
        />
      </View>

      {item.argv && item.argv.length > 0 ? <CommandBox argv={item.argv} /> : null}

      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: 6 }}>
        {item.cwd ? (
          <View style={{ flexDirection: "row", alignItems: "center", gap: 4, maxWidth: 280 }}>
            <Icon name="Folder" size={11} color={colors.foregroundMuted} />
            <Text
              numberOfLines={1}
              ellipsizeMode="middle"
              style={{ color: colors.foregroundMuted, fontSize: 11, fontFamily }}
            >
              {item.cwd}
            </Text>
          </View>
        ) : null}
        <Text style={{ color: colors.foregroundMuted, fontSize: 11 }}>
          {isCompleted
            ? isSuccess
              ? "Executed successfully"
              : `Failed with exit ${item.exit}`
            : isConfirming
              ? "Step 1 approved · Awaiting 2nd confirmation…"
              : "Executing on host…"}
        </Text>
      </View>
    </Card>
  );
}

function RecentItem({
  item,
}: {
  item: {
    id: string;
    argv: string[];
    cwd: string;
    decision: string;
    by: string;
    exit: number;
    output: string;
  };
}) {
  const { colors } = usePluginTheme();
  const approved = item.decision === "approve";
  const executed = item.exit >= 0;
  const failed = executed && item.exit !== 0;
  const preview =
    item.output.length > OUTPUT_PREVIEW
      ? `${item.output.slice(0, OUTPUT_PREVIEW)}\n…[truncated]`
      : item.output;
  const [program] = item.argv;
  const statusColor = !approved
    ? colors.statusDanger
    : failed
      ? colors.statusWarning
      : colors.statusSuccess;

  const fontFamily = Platform.select({ ios: "Menlo", android: "monospace", default: "monospace" });

  return (
    <Card
      style={{
        borderLeftWidth: 3,
        borderLeftColor: statusColor,
        gap: 6,
        padding: 10,
      }}
    >
      <View style={{ flexDirection: "row", alignItems: "center", justifyContent: "space-between", gap: 8 }}>
        <View style={{ flexDirection: "row", alignItems: "center", gap: 6, flex: 1 }}>
          <Icon
            name={approved ? (failed ? "AlertTriangle" : "Check") : "X"}
            size={13}
            color={statusColor}
          />
          <Text style={{ color: colors.foreground, fontSize: 13, fontWeight: "600" }}>
            {program ?? "(empty)"}
          </Text>
          <Text style={{ color: colors.foregroundMuted, fontSize: 11 }}>
            · {item.by ? `by ${item.by}` : "local"}
          </Text>
        </View>
        <Badge
          label={approved ? (executed ? `exit ${item.exit}` : "approved") : "denied"}
          variant={approved ? (failed ? "warning" : "success") : "danger"}
          styleVariant="tinted"
        />
      </View>

      <CommandBox argv={item.argv} />

      <View style={{ flexDirection: "row", alignItems: "center", gap: 4 }}>
        <Icon name="Folder" size={11} color={colors.foregroundMuted} />
        <Text
          numberOfLines={1}
          ellipsizeMode="middle"
          style={{ color: colors.foregroundMuted, fontSize: 11, fontFamily }}
        >
          {item.cwd}
        </Text>
      </View>

      {executed && preview.trim().length > 0 ? (
        <Collapsible
          title={failed ? `Output · exit ${item.exit}` : "Output"}
          icon="ScrollText"
          initiallyExpanded={false}
        >
          <CodeBlock code={preview} maxHeight={180} />
        </Collapsible>
      ) : null}
    </Card>
  );
}

function TelegramStatusBar({ onOpenSettings }: { onOpenSettings(): void }) {
  const { colors } = usePluginTheme();
  const tg = useTelegramInfo();
  const info = tg.data;

  if (tg.isPending && !info) return null;

  const isConnected = info?.status === "connected" || Boolean(info?.configured);
  const badgeVariant = isConnected ? "success" : info?.status === "error" ? "danger" : "neutral";

  return (
    <View
      style={{
        flexDirection: "row",
        alignItems: "center",
        justifyContent: "space-between",
        backgroundColor: colors.surface1,
        borderColor: colors.border,
        borderWidth: 1,
        borderRadius: 6,
        paddingHorizontal: 10,
        paddingVertical: 6,
        gap: 8,
      }}
    >
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6, flex: 1 }}>
        <Icon
          name="Send"
          size={12}
          color={isConnected ? colors.statusSuccess : colors.foregroundMuted}
        />
        <Text style={{ color: colors.foreground, fontSize: 12, fontWeight: "600" }}>
          Telegram {isConnected ? "Active" : "Fallback"}
        </Text>
        {info?.botUsername ? (
          <Text style={{ color: colors.foregroundMuted, fontSize: 11 }}>
            @{info.botUsername}
          </Text>
        ) : null}
        {info?.chatId ? (
          <Text style={{ color: colors.foregroundMuted, fontSize: 11 }}>
            · Chat {info.chatId}
          </Text>
        ) : null}
      </View>
      <View style={{ flexDirection: "row", alignItems: "center", gap: 6 }}>
        <Badge
          label={isConnected ? "connected" : "unconfigured"}
          variant={badgeVariant}
          styleVariant="tinted"
        />
        <Pressable
          accessibilityRole="button"
          accessibilityLabel="Configure Telegram settings"
          onPress={onOpenSettings}
          style={({ pressed }) => ({ opacity: pressed ? 0.6 : 1, padding: 2 })}
        >
          <Icon name="Settings" size={13} color={colors.foregroundMuted} />
        </Pressable>
      </View>
    </View>
  );
}

export function ApprovalSurface({
  theme,
  layout,
  onOpenSettings,
}: PluginSurfaceProps & { onOpenSettings(): void }) {
  const toast = useToast();
  const query = usePendingList();
  const recent = useRecentList();
  const socketPath = useSocketPath();
  const decide = useRpc(verdict);
  const getStatus = useRpc(approvalStatus);
  const queryClient = useQueryClient();
  const isMountedRef = useRef(true);

  useEffect(() => {
    isMountedRef.current = true;
    return () => {
      isMountedRef.current = false;
    };
  }, []);

  const [decidingMap, setDecidingMap] = useState<Record<string, "approve" | "deny">>({});
  const [activeExecutions, setActiveExecutions] = useState<
    Record<
      string,
      {
        id: string;
        decision: "approve" | "deny";
        status:
          | "running"
          | "confirming"
          | "completed"
          | "denied"
          | "timeout"
          | "not_found"
          | "client_aborted"
          | "confirmation_timeout";
        argv?: string[];
        cwd?: string;
        exit?: number;
        output?: string;
      }
    >
  >({});

  const handleDecide = async (
    item: { id: string; argv: string[]; cwd: string },
    decision: "approve" | "deny",
  ) => {
    const { id } = item;
    setDecidingMap((prev) => ({ ...prev, [id]: decision }));

    try {
      const res = await decide({ id, decision, socketPath });
      if (!isMountedRef.current) return;

      if (!res.recorded) {
        setDecidingMap((prev) => {
          const next = { ...prev };
          delete next[id];
          return next;
        });
        toast.error("Already decided or 2fadod unreachable.");
        void queryClient.invalidateQueries({ queryKey: LIST_KEY });
        void queryClient.invalidateQueries({ queryKey: RECENT_KEY });
        return;
      }

      setDecidingMap((prev) => {
        const next = { ...prev };
        delete next[id];
        return next;
      });
      void queryClient.invalidateQueries({ queryKey: LIST_KEY });

      if (decision === "deny") {
        toast.show("Denied command", { variant: "default" });
        void queryClient.invalidateQueries({ queryKey: RECENT_KEY });
        return;
      }

      // If approved, track in activeExecutions and poll status
      setActiveExecutions((prev) => ({
        ...prev,
        [id]: {
          id,
          decision: "approve",
          status: "running",
          argv: item.argv,
          cwd: item.cwd,
        },
      }));

      // Start targeted polling for this request
      void (async () => {
        await new Promise((r) => setTimeout(r, 250));
        const maxAttempts = 60; // 60 * 350ms ~ 21s
        for (let attempt = 0; attempt < maxAttempts; attempt++) {
          if (!isMountedRef.current) return;
          try {
            const statusRes = await getStatus({ id, socketPath });
            if (!isMountedRef.current) return;

            if (statusRes.status === "confirming") {
              setActiveExecutions((prev) => ({
                ...prev,
                [id]: {
                  id,
                  decision: "approve",
                  status: "confirming",
                  argv: statusRes.argv ?? item.argv,
                  cwd: statusRes.cwd ?? item.cwd,
                },
              }));
              void queryClient.invalidateQueries({ queryKey: LIST_KEY });
            }

            if (statusRes.status === "completed") {
              setActiveExecutions((prev) => ({
                ...prev,
                [id]: {
                  id,
                  decision: "approve",
                  status: "completed",
                  argv: statusRes.argv ?? item.argv,
                  cwd: statusRes.cwd ?? item.cwd,
                  exit: statusRes.exit,
                  output: statusRes.output,
                },
              }));
              void queryClient.invalidateQueries({ queryKey: RECENT_KEY });

              const prog = statusRes.argv?.[0] ?? item.argv[0] ?? "Command";
              if (statusRes.exit === 0) {
                toast.show(`${prog} completed (exit 0)`, { variant: "success" });
              } else {
                toast.show(`${prog} failed (exit ${statusRes.exit})`, { variant: "warning" });
              }

              // Keep card visible briefly to show final exit badge, then clean up
              setTimeout(() => {
                if (!isMountedRef.current) return;
                setActiveExecutions((prev) => {
                  const next = { ...prev };
                  delete next[id];
                  return next;
                });
              }, 800);
              return;
            }

            if (
              statusRes.status === "denied" ||
              statusRes.status === "timeout" ||
              statusRes.status === "not_found" ||
              statusRes.status === "client_aborted" ||
              statusRes.status === "confirmation_timeout"
            ) {
              void queryClient.invalidateQueries({ queryKey: LIST_KEY });
              void queryClient.invalidateQueries({ queryKey: RECENT_KEY });
              setActiveExecutions((prev) => {
                const next = { ...prev };
                delete next[id];
                return next;
              });
              if (statusRes.status === "confirmation_timeout") {
                toast.error("Confirmation timed out — command cancelled");
              } else if (statusRes.status === "client_aborted") {
                toast.error("Calling process aborted command");
              } else if (statusRes.status === "timeout") {
                toast.error("Execution timed out");
              }
              return;
            }
          } catch {
            // Transient error; continue polling
          }
          await new Promise((r) => setTimeout(r, 350));
        }

        // Timeout polling fallback
        if (isMountedRef.current) {
          void queryClient.invalidateQueries({ queryKey: RECENT_KEY });
          setActiveExecutions((prev) => {
            const next = { ...prev };
            delete next[id];
            return next;
          });
        }
      })();
    } catch {
      if (!isMountedRef.current) return;
      setDecidingMap((prev) => {
        const next = { ...prev };
        delete next[id];
        return next;
      });
      toast.error("Verdict failed — 2fadod unreachable.");
    }
  };

  useNewPendingToast(query.data?.items);

  const items = query.data?.items ?? [];
  const recentItems = recent.data?.items ?? [];
  const activeList = Object.values(activeExecutions);

  return (
    <PluginThemeProvider theme={{ colors: theme.colors }} layout={layout}>
      <ScrollView
        style={{ flex: 1, backgroundColor: theme.colors.surface0 }}
        contentContainerStyle={{
          alignItems: "center",
          padding: layout.compact ? 12 : 20,
        }}
      >
        <View style={{ width: "100%", maxWidth: 640, alignSelf: "center", gap: 10 }}>
          <View
            style={{
              flexDirection: "row",
              alignItems: "center",
              justifyContent: "space-between",
              paddingVertical: 6,
              borderBottomWidth: 1,
              borderBottomColor: theme.colors.border,
              marginBottom: 2,
            }}
          >
            <View style={{ flexDirection: "row", alignItems: "center", gap: 10 }}>
              <View
                style={{
                  width: 30,
                  height: 30,
                  borderRadius: 6,
                  backgroundColor: theme.colors.surface1,
                  alignItems: "center",
                  justifyContent: "center",
                  borderWidth: 1,
                  borderColor: theme.colors.border,
                }}
              >
                <Icon name="ShieldCheck" size={17} color={theme.colors.accent} />
              </View>
              <View>
                <Text style={{ color: theme.colors.foreground, fontSize: 15, fontWeight: "700" }}>
                  2fado approvals
                </Text>
                <Text style={{ color: theme.colors.foregroundMuted, fontSize: 11 }}>
                  Privileged command gating
                </Text>
              </View>
            </View>
            <Button
              label="Settings"
              variant="secondary"
              size="sm"
              icon="Settings"
              accessibilityLabel="Open 2fado settings"
              onPress={onOpenSettings}
            />
          </View>

          <TelegramStatusBar onOpenSettings={onOpenSettings} />

          <SectionHeader title="Pending" count={query.data ? items.length : undefined} />
          {query.isPending ? (
            <Text style={{ color: theme.colors.foregroundMuted, fontSize: 12 }}>Loading…</Text>
          ) : null}
          {query.isError ? (
            <Card style={{ borderLeftWidth: 3, borderLeftColor: theme.colors.statusDanger }}>
              <View style={{ flexDirection: "row", alignItems: "center", gap: 8 }}>
                <Icon name="CloudOff" size={16} color={theme.colors.statusDanger} />
                <Text style={{ color: theme.colors.foreground, fontSize: 13, fontWeight: "600" }}>
                  2fadod unreachable
                </Text>
              </View>
              <Text style={{ color: theme.colors.foregroundMuted, fontSize: 12, marginTop: 4 }}>
                Check the socket path in settings.
              </Text>
            </Card>
          ) : null}
          {!query.isPending && !query.isError && items.length === 0 && activeList.length === 0 ? (
            <EmptyState
              icon="ShieldCheck"
              title="All clear"
              description="No commands waiting for authorization."
            />
          ) : null}
          {items.map((item) => (
            <ApprovalItem
              key={item.id}
              item={item}
              deciding={decidingMap[item.id]}
              onDecide={(target, decision) => handleDecide(target, decision)}
            />
          ))}

          {activeList.length > 0 ? (
            <>
              <SectionHeader title="In flight" count={activeList.length} />
              {activeList.map((item) => (
                <ExecutingItem key={item.id} item={item} />
              ))}
            </>
          ) : null}

          <SectionHeader title="Recent" />
          {recent.isPending ? (
            <Text style={{ color: theme.colors.foregroundMuted, fontSize: 12 }}>Loading…</Text>
          ) : null}
          {!recent.isPending && recentItems.length === 0 ? (
            <EmptyState
              icon="History"
              title="No history yet"
              description="Decided requests will appear here with execution status and output."
            />
          ) : null}
          {recentItems.map((item) => (
            <RecentItem key={item.id} item={item} />
          ))}
        </View>
      </ScrollView>
    </PluginThemeProvider>
  );
}
