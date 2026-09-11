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
  CardHeader,
  CodeBlock,
  PluginThemeProvider,
  usePluginSettings,
  usePluginTheme,
} from "paseo-plugin-helper/client";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { Text, View } from "react-native";
import { approvalSettings, pendingList, recentList, verdict } from "../shared/approval";

const LIST_KEY = ["twofado", "pending"];
const RECENT_KEY = ["twofado", "recent"];
const POLL_MS = 3000;
const RECENT_POLL_MS = 5000;
const RECENT_LIMIT = 10;
const OUTPUT_PREVIEW = 2000;

const seenIds = new Set<string>();
const SEEN_IDS_CAP = 500;

function useNewPendingToast(items: Array<{ id: string }> | undefined) {
  const toast = useToast();
  useEffect(() => {
    const fresh = (items ?? []).map((item) => item.id).filter((id) => !seenIds.has(id));
    if (fresh.length === 0) return;
    for (const id of fresh) seenIds.add(id);
    while (seenIds.size > SEEN_IDS_CAP) {
      const oldest = seenIds.values().next();
      if (oldest.done) break;
      seenIds.delete(oldest.value);
    }
    toast.show(
      fresh.length === 1 ? "2fado approval needed" : `${fresh.length} 2fado approvals needed`,
      { variant: "warning" },
    );
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

function ApprovalItem({
  item,
  pending,
  onDecide,
}: {
  item: { id: string; argv: string[]; host: string; caller: string; cwd: string; expiresIn: number };
  pending: boolean;
  onDecide(id: string, decision: "approve" | "deny"): void;
}) {
  const { colors } = usePluginTheme();
  return (
    <Card>
      <CardHeader
        title={item.argv.map((arg) => JSON.stringify(arg)).join(" ")}
        subtitle={`${item.host} · caller ${item.caller} · expires in ${item.expiresIn}s`}
        icon="Terminal"
      />
      <Text style={{ color: colors.foregroundMuted, fontSize: 12 }}>{item.cwd}</Text>
      <View style={{ flexDirection: "row", gap: 8 }}>
        <View style={{ flex: 1 }}>
          <Button
            label="Approve"
            variant="primary"
            accessibilityLabel={`Approve ${item.id}`}
            disabled={pending}
            onPress={() => onDecide(item.id, "approve")}
          />
        </View>
        <View style={{ flex: 1 }}>
          <Button
            label="Deny"
            variant="danger"
            accessibilityLabel={`Deny ${item.id}`}
            disabled={pending}
            onPress={() => onDecide(item.id, "deny")}
          />
        </View>
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
  const preview =
    item.output.length > OUTPUT_PREVIEW
      ? `${item.output.slice(0, OUTPUT_PREVIEW)}\n…[truncated]`
      : item.output;
  return (
    <Card>
      <CardHeader
        title={item.argv.map((arg) => JSON.stringify(arg)).join(" ")}
        subtitle={`${item.decision} · ${executed ? `exit ${item.exit}` : "not executed"} · by ${item.by || "?"}`}
        icon="Terminal"
      />
      <Text style={{ color: colors.foregroundMuted, fontSize: 12 }}>{item.cwd}</Text>
      <Badge
        label={approved ? (executed && item.exit !== 0 ? `approved · exit ${item.exit}` : "approved") : item.decision}
        variant={approved ? (executed && item.exit !== 0 ? "warning" : "success") : "danger"}
        icon={approved ? "Check" : "X"}
      />
      {executed ? (
        preview.length > 0 ? (
          <CodeBlock code={preview} />
        ) : (
          <Text style={{ color: colors.foregroundMuted, fontSize: 12 }}>(no output)</Text>
        )
      ) : (
        <Text style={{ color: colors.foregroundMuted, fontSize: 12 }}>
          No output — denied before execution.
        </Text>
      )}
    </Card>
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
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: (input: { id: string; decision: "approve" | "deny" }) =>
      decide({ ...input, socketPath }),
    onSuccess: (result, input) => {
      if (result.recorded) {
        toast.show(input.decision === "approve" ? "Approved — executing" : "Denied", {
          variant: input.decision === "approve" ? "success" : "default",
        });
      } else {
        toast.error("Already decided or 2fadod unreachable.");
      }
      void queryClient.invalidateQueries({ queryKey: LIST_KEY });
      void queryClient.invalidateQueries({ queryKey: RECENT_KEY });
    },
    onError: () => toast.error("Verdict failed — 2fadod unreachable."),
  });

  useNewPendingToast(query.data?.items);

  const items = query.data?.items ?? [];
  const recentItems = recent.data?.items ?? [];

  return (
    <PluginThemeProvider theme={{ colors: theme.colors }} layout={layout}>
      <ScrollView style={{ flex: 1, backgroundColor: theme.colors.surface0 }}>
        <View style={{ padding: layout.compact ? 16 : 24, gap: 10 }}>
          <Card>
            <CardHeader title="2fado approvals" icon="ShieldCheck" />
            <Text style={{ color: theme.colors.foregroundMuted, fontSize: 12 }}>
              Pending 2fado requests wait here until you approve or deny them. Nothing listed
              means nothing waiting. Anyone holding this app can approve — real gating belongs
              in 2fadod.
            </Text>
            <Button
              label="2fado settings: socket path, Telegram fallback"
              variant="secondary"
              accessibilityLabel="Open 2fado settings"
              onPress={onOpenSettings}
            />
          </Card>
          {query.isPending ? (
            <Text style={{ color: theme.colors.foreground, fontSize: 13 }}>Loading…</Text>
          ) : null}
          {query.isError ? (
            <Text style={{ color: theme.colors.foreground, fontSize: 13 }}>
              2fadod unreachable — check the socket path in settings.
            </Text>
          ) : null}
          {!query.isPending && !query.isError && items.length === 0 ? (
            <Text style={{ color: theme.colors.foregroundMuted, fontSize: 12 }}>
              Nothing pending.
            </Text>
          ) : null}
          {items.map((item) => (
            <ApprovalItem
              key={item.id}
              item={item}
              pending={mutation.isPending}
              onDecide={(id, decision) => mutation.mutate({ id, decision })}
            />
          ))}
          <Card>
            <CardHeader title="Recent approvals" icon="History" />
            <Text style={{ color: theme.colors.foregroundMuted, fontSize: 12 }}>
              Decided requests with their exit status and execution output.
            </Text>
          </Card>
          {recent.isPending ? (
            <Text style={{ color: theme.colors.foreground, fontSize: 13 }}>Loading…</Text>
          ) : null}
          {!recent.isPending && recentItems.length === 0 ? (
            <Text style={{ color: theme.colors.foregroundMuted, fontSize: 12 }}>
              Nothing decided yet.
            </Text>
          ) : null}
          {recentItems.map((item) => (
            <RecentItem key={item.id} item={item} />
          ))}
        </View>
      </ScrollView>
    </PluginThemeProvider>
  );
}
