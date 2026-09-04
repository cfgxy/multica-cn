import { useState } from "react";
import { Pressable, View } from "react-native";
// RN 0.83 edge-to-edge 下 Android 的窗口 resize 失效，避让统一走
// keyboard-controller（behavior="padding" 两端一致），见 RUYI-30。
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { SafeAreaView } from "react-native-safe-area-context";
import { router } from "expo-router";
import * as Haptics from "expo-haptics";
import { Text } from "@/components/ui/text";
import { TextField } from "@/components/ui/text-field";
import { Button } from "@/components/ui/button";
import { MulticaLogo } from "@/components/brand/multica-logo";
import { useAuthStore } from "@/data/auth-store";
import { mapAuthError } from "@/lib/auth-error";
import { useT } from "@/lib/use-t";

export default function Login() {
  const { t } = useT("auth");
  const sendCode = useAuthStore((s) => s.sendCode);
  const [email, setEmail] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const onSubmit = async () => {
    const trimmed = email.trim();
    if (!trimmed) return;
    void Haptics.selectionAsync();
    setSubmitting(true);
    setError(null);
    try {
      await sendCode(trimmed);
      router.push({ pathname: "/verify", params: { email: trimmed } });
    } catch (err) {
      void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Error);
      setError(
        mapAuthError(
          err,
          t("mobile.errors.send_failed", "Couldn't send the code. Try again."),
        ),
      );
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <SafeAreaView className="flex-1 bg-background">
      <KeyboardAvoidingView className="flex-1" behavior="padding">
        <View className="flex-1 justify-center px-6 gap-6">
          <View className="items-center gap-3">
            <MulticaLogo size={32} />
            <View className="gap-1 items-center">
              <Text className="text-2xl font-semibold text-foreground">
                {t("signin.title", "Sign in to Multica")}
              </Text>
              <Text className="text-sm text-muted-foreground text-center">
                {t("signin.description", "Enter your email to get a login code")}
              </Text>
            </View>
          </View>

          <View className="gap-3">
            <TextField
              autoCapitalize="none"
              autoComplete="email"
              autoFocus
              keyboardType="email-address"
              placeholder={t("common.email_placeholder", "you@example.com")}
              value={email}
              onChangeText={setEmail}
              onSubmitEditing={onSubmit}
              returnKeyType="send"
              editable={!submitting}
              invalid={!!error}
            />
            {error ? (
              <Text className="text-sm text-destructive">{error}</Text>
            ) : null}
          </View>

          <Button
            size="lg"
            disabled={submitting || !email.trim()}
            onPress={onSubmit}
          >
            <Text>
              {submitting
                ? t("signin.sending", "Sending code...")
                : t("mobile.send_code", "Send code")}
            </Text>
          </Button>
        </View>

        {/* 服务器设置入口 —— 弱视觉、贴底,不抢 Send code 的注意力。
            自建后端用户必须能在登录前改地址(RUYI-4)。 */}
        <Pressable
          onPress={() => router.push("/server-settings")}
          className="items-center pb-4 active:opacity-60"
        >
          <Text className="text-xs text-muted-foreground">
            {t("mobile.server_settings", "Server settings")}
          </Text>
        </Pressable>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}
