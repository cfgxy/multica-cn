/**
 * Add squad member (`more/squads/[id]/add-member`, RUYI-346 S2) — formSheet
 * route shell; picker logic lives in `MemberAddSheet`.
 */
import { useLocalSearchParams } from "expo-router";
import { View } from "react-native";
import { MemberAddSheet } from "@/components/squads/member-add-sheet";

export default function AddSquadMemberScreen() {
  const { id } = useLocalSearchParams<{ id: string }>();
  const squadId = typeof id === "string" ? id : "";

  return (
    <View className="flex-1 bg-background">
      <MemberAddSheet squadId={squadId} />
    </View>
  );
}
