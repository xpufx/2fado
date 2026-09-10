import { useRpc, useSettings } from "@getpaseo/plugin/client";
import type {
  PluginButtonContentProps,
  PluginButtonIconProps,
  PluginButtonRegistration,
} from "@getpaseo/plugin/client";
import { Icon, ScrollView, useToast } from "@getpaseo/plugin/client/react-native";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo } from "react";
import { Pressable, Text, View } from "react-native";
import { approvalSettings, pendingList, verdict } from "../shared/approval";

const LIST_KEY = ["2fado-approval", "pending"];
const POLL_MS = 3000;

const seenIds = new Set<string>();
const pillRegistry = new Map<string, PluginButtonRegistration>();

export function trackPill(agentId: string, registration: PluginButtonRegistration) {
  pillRegistry.get(agentId)?.remove();
  pillRegistry.set(agentId, registration);
}

export function untrackPill(agentId: string) {
  pillRegistry.get(agentId)?.remove();
  pillRegistry.delete(agentId);
}

export function usePendingList() {
  const list = useRpc(pendingList);
  return useQuery({
    queryKey: LIST_KEY,
    queryFn: () => list({}),
    refetchInterval: POLL_MS,
    retry: false,
  });
}

export function ApprovalPillIcon(props: PluginButtonIconProps) {
  const { theme } = props;
  const agentId = props.context === "agent" ? props.agentId : "";
  const size = props.size;
  const color = props.color;
  const toast = useToast();
  const { data } = usePendingList();
  const count = data?.items.length ?? 0;

  useEffect(() => {
    const fresh = (data?.items ?? []).map((item) => item.id).filter((id) => !seenIds.has(id));
    if (fresh.length === 0) return;
    for (const id of fresh) seenIds.add(id);
    toast.show(
      fresh.length === 1 ? "2fado approval needed" : `${fresh.length} 2fado approvals needed`,
      { variant: "warning" },
    );
  }, [data, toast]);

  useEffect(() => {
    pillRegistry.get(agentId)?.update({ label: count > 0 ? `${count} pending` : "2fado" });
  }, [agentId, count]);

  const badge = useMemo(
    () => ({
      wrap: {
        minWidth: size + 8,
        paddingHorizontal: 4,
        borderRadius: 9,
        backgroundColor: theme.colors.statusWarning,
        alignItems: "center" as const,
      },
      text: { color: theme.colors.accentForeground, fontSize: 12, fontWeight: "700" as const },
    }),
    [size, theme],
  );
  if (count === 0) return <Icon name="ShieldCheck" size={size} color={color} />;
  return (
    <View style={badge.wrap}>
      <Text style={badge.text}>{count}</Text>
    </View>
  );
}

function ApprovalItem({
  theme,
  compact,
  item,
  configured,
  pending,
  onDecide,
}: {
  theme: PluginButtonContentProps["theme"];
  compact: boolean;
  item: { id: string; argv: string[]; host: string; caller: string; cwd: string; expiresIn: number };
  configured: boolean;
  pending: boolean;
  onDecide(id: string, decision: "approve" | "deny"): void;
}) {
  const styles = useMemo(
    () => ({
      card: {
        padding: compact ? 12 : 16,
        borderRadius: 10,
        backgroundColor: theme.colors.surface1,
        borderWidth: 1,
        borderColor: theme.colors.border,
        gap: 8,
      },
      argv: { color: theme.colors.foreground, fontSize: compact ? 13 : 14 },
      meta: { color: theme.colors.foregroundMuted, fontSize: 12 },
      row: { flexDirection: "row" as const, gap: 8 },
      approve: {
        flex: 1,
        padding: 10,
        borderRadius: 8,
        backgroundColor: theme.colors.accent,
        opacity: configured && !pending ? 1 : 0.5,
      },
      deny: {
        flex: 1,
        padding: 10,
        borderRadius: 8,
        borderWidth: 1,
        borderColor: theme.colors.statusDanger,
        opacity: configured && !pending ? 1 : 0.5,
      },
      approveText: { color: theme.colors.accentForeground, textAlign: "center" as const },
      denyText: { color: theme.colors.statusDanger, textAlign: "center" as const },
    }),
    [theme, compact, configured, pending],
  );
  const disabled = !configured || pending;
  return (
    <View style={styles.card}>
      <Text style={styles.argv}>{item.argv.map((arg) => JSON.stringify(arg)).join(" ")}</Text>
      <Text style={styles.meta}>
        {item.host} · caller {item.caller} · expires in {item.expiresIn}s
      </Text>
      <Text style={styles.meta}>{item.cwd}</Text>
      <View style={styles.row}>
        <Pressable
          accessibilityRole="button"
          accessibilityLabel={`Approve ${item.id}`}
          disabled={disabled}
          style={styles.approve}
          onPress={() => onDecide(item.id, "approve")}
        >
          <Text style={styles.approveText}>Approve</Text>
        </Pressable>
        <Pressable
          accessibilityRole="button"
          accessibilityLabel={`Deny ${item.id}`}
          disabled={disabled}
          style={styles.deny}
          onPress={() => onDecide(item.id, "deny")}
        >
          <Text style={styles.denyText}>Deny</Text>
        </Pressable>
      </View>
    </View>
  );
}

export function ApprovalPopover({ theme, layout }: PluginButtonContentProps) {
  const toast = useToast();
  const query = usePendingList();
  const settings = useSettings(approvalSettings);
  const decide = useRpc(verdict);
  const queryClient = useQueryClient();
  const mutation = useMutation({
    mutationFn: (input: { id: string; decision: "approve" | "deny" }) => decide(input),
    onSuccess: (result, input) => {
      if (result.recorded) {
        toast.show(input.decision === "approve" ? "Approved — executing" : "Denied", {
          variant: input.decision === "approve" ? "success" : "default",
        });
      } else {
        toast.error("Already decided or 2fadod unreachable.");
      }
      void queryClient.invalidateQueries({ queryKey: LIST_KEY });
    },
    onError: () => toast.error("Verdict failed — 2fadod unreachable."),
  });

  const styles = useMemo(
    () => ({
      wrap: { padding: layout.compact ? 12 : 16, gap: 10, minWidth: 280 },
      title: { color: theme.colors.foreground, fontSize: layout.compact ? 16 : 18 },
      body: { color: theme.colors.foreground, fontSize: 13 },
      hint: { color: theme.colors.foregroundMuted, fontSize: 12 },
    }),
    [theme, layout.compact],
  );

  const configured =
    settings.status === "ready" && settings.values.approvers.trim().length > 0;
  const items = query.data?.items ?? [];

  return (
    <ScrollView style={{ flexGrow: 0 }}>
      <View style={styles.wrap}>
        <Text style={styles.title}>2fado approvals</Text>
        {settings.status === "ready" && !configured ? (
          <Text style={styles.hint}>
            No approvers configured — approvals disabled. Set them in Settings → Plugins →
            2fado approval.
          </Text>
        ) : null}
        {query.isPending ? <Text style={styles.body}>Loading…</Text> : null}
        {query.isError ? (
          <Text style={styles.body}>2fadod unreachable — check the socket path in settings.</Text>
        ) : null}
        {!query.isPending && !query.isError && items.length === 0 ? (
          <Text style={styles.hint}>Nothing pending.</Text>
        ) : null}
        {items.map((item) => (
          <ApprovalItem
            key={item.id}
            theme={theme}
            compact={layout.compact}
            item={item}
            configured={configured}
            pending={mutation.isPending}
            onDecide={(id, decision) => mutation.mutate({ id, decision })}
          />
        ))}
      </View>
    </ScrollView>
  );
}
