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
  PluginThemeProvider,
  usePluginSettings,
  usePluginTheme,
} from "paseo-plugin-helper/client";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect } from "react";
import { Text, View } from "react-native";
import { approvalSettings, pendingList, verdict } from "../shared/approval";

const LIST_KEY = ["twofado", "pending"];
const POLL_MS = 3000;

const seenIds = new Set<string>();
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

export function ApprovalHeaderIcon(props: PluginButtonIconProps) {
  const { theme } = props;
  const workspaceId = props.workspaceId;
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

export function ApprovalSurface({
  theme,
  layout,
  onOpenSettings,
}: PluginSurfaceProps & { onOpenSettings(): void }) {
  const toast = useToast();
  const query = usePendingList();
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
    },
    onError: () => toast.error("Verdict failed — 2fadod unreachable."),
  });

  const items = query.data?.items ?? [];

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
        </View>
      </ScrollView>
    </PluginThemeProvider>
  );
}
