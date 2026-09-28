import { Redirect, Stack } from "expo-router";
import { useStartupServerStore } from "@/data/startup-server-store";

export default function AuthLayout() {
  const phase = useStartupServerStore((s) => s.phase);
  if (phase !== "ready") return <Redirect href={phase === "select" ? "/servers/select" : "/"} />;
  return <Stack screenOptions={{ headerShown: false }} />;
}
