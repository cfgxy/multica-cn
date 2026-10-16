import { useQuery } from "@tanstack/react-query";
import { Pressable, View } from "react-native";
import { Ionicons } from "@expo/vector-icons";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Text } from "@/components/ui/text";
import { resolveRuntimeModels } from "@/lib/runtime-discovery";
import { useColorScheme } from "@/lib/use-color-scheme";
import { THEME } from "@/lib/theme";
import { useT } from "@/lib/use-t";

interface Props {
  runtimeId: string | null;
  modelOverride: string | null;
  agentModel: string | null;
  disabled: boolean;
  onSelect: (model: string | null) => Promise<void>;
}

export function ChatSessionModelPicker({ runtimeId, modelOverride, agentModel, disabled, onSelect }: Props) {
  const { t } = useT("chat");
  const { colorScheme } = useColorScheme();
  const theme = THEME[colorScheme];
  const models = useQuery({
    queryKey: ["chat", "runtime-models", runtimeId ?? ""],
    queryFn: () => resolveRuntimeModels(runtimeId!),
    enabled: !!runtimeId,
    staleTime: 60_000,
  });
  const currentLabel = modelOverride ?? agentModel ?? t("mobile.model.agent_default", "Agent default");
  const selectModel = async (model: string | null) => {
    if (disabled) return;
    await onSelect(model);
  };

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Pressable
          disabled={disabled}
          accessibilityRole="button"
          accessibilityLabel={t("mobile.model.current", "Current model: {{model}}", { model: currentLabel })}
          className="mx-3 my-1 min-h-10 flex-row items-center gap-2 self-start rounded-md border border-border bg-background px-3 active:opacity-70 disabled:opacity-50"
        >
          <Ionicons name="hardware-chip-outline" size={16} color={theme.mutedForeground} />
          <Text numberOfLines={1} className="max-w-64 text-xs text-foreground">{currentLabel}</Text>
          <Ionicons name="chevron-down" size={14} color={theme.mutedForeground} />
        </Pressable>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className="max-h-80 w-72">
        <DropdownMenuItem disabled={disabled} onPress={() => void selectModel(null)}>
          <View className="flex-1">
            <Text>{t("mobile.model.agent_default", "Agent default")}</Text>
            <Text className="text-xs text-muted-foreground">{agentModel || t("mobile.model.runtime_default", "Runtime default")}</Text>
          </View>
          {modelOverride === null ? <Ionicons name="checkmark" size={16} color={theme.brand} /> : null}
        </DropdownMenuItem>
        {models.data?.supported ? models.data.models.map((model) => (
          <DropdownMenuItem key={model.id} disabled={disabled} onPress={() => void selectModel(model.id)}>
            <Text className="flex-1">{model.label || model.id}</Text>
            {modelOverride === model.id ? <Ionicons name="checkmark" size={16} color={theme.brand} /> : null}
          </DropdownMenuItem>
        )) : null}
        {models.isLoading ? <DropdownMenuItem disabled><Text>{t("mobile.model.loading", "Loading models…")}</Text></DropdownMenuItem> : null}
        {models.isError ? <DropdownMenuItem disabled><Text>{t("mobile.model.load_failed", "Could not load models")}</Text></DropdownMenuItem> : null}
        {models.data?.supported === false ? <DropdownMenuItem disabled><Text>{t("mobile.model.unsupported", "This runtime does not support model selection")}</Text></DropdownMenuItem> : null}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
