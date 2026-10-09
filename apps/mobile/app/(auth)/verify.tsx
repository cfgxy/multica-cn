import { useEffect, useRef, useState } from "react";
import { Pressable, View } from "react-native";
import { KeyboardAvoidingView } from "react-native-keyboard-controller";
import { SafeAreaView } from "react-native-safe-area-context";
import { router, useLocalSearchParams } from "expo-router";
import * as Haptics from "expo-haptics";
import { Text } from "@/components/ui/text";
import { OtpInput, type OtpInputRef } from "@/components/ui/otp-input";
import { Button } from "@/components/ui/button";
import { MulticaLogo } from "@/components/brand/multica-logo";
import { useAuthStore } from "@/data/auth-store";
import { mapAuthError } from "@/lib/auth-error";
import { useT } from "@/lib/use-t";

const CODE_LENGTH = 6;
const RESEND_COOLDOWN_SECONDS = 60;

export default function Verify() {
  const { t } = useT("auth");
  const sendCode = useAuthStore((s) => s.sendCode);
  const verifyCode = useAuthStore((s) => s.verifyCode);
  const { email = "" } = useLocalSearchParams<{ email?: string }>();
  const [code, setCode] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [cooldown, setCooldown] = useState(RESEND_COOLDOWN_SECONDS);
  const [resending, setResending] = useState(false);
  const otpRef = useRef<OtpInputRef>(null);
  // RUYI-568 取消语义：返回修改邮箱（或任何方式离开本屏）时中止在途的
  // verify/resend 请求——不留悬挂请求，也不会在用户已离开后突然导航。
  const resendAbortRef = useRef<AbortController | null>(null);
  const verifyAbortRef = useRef<AbortController | null>(null);

  useEffect(() => {
    return () => {
      resendAbortRef.current?.abort();
      verifyAbortRef.current?.abort();
    };
  }, []);

  useEffect(() => {
    if (cooldown <= 0) return;
    const t = setInterval(() => {
      setCooldown((c) => (c <= 1 ? 0 : c - 1));
    }, 1000);
    return () => clearInterval(t);
  }, [cooldown]);

  const submit = async (value: string) => {
    if (!value || !email || submitting) return;
    void Haptics.selectionAsync();
    setSubmitting(true);
    setError(null);
    const controller = new AbortController();
    verifyAbortRef.current = controller;
    try {
      await verifyCode(email, value, { signal: controller.signal });
      void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Success);
      router.replace("/");
    } catch (err) {
      if (!controller.signal.aborted) {
        void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Error);
        setError(
          mapAuthError(
            err,
            t(
              "mobile.errors.verify_failed",
              "Couldn't verify the code. Try again.",
            ),
          ),
        );
        setSubmitting(false);
        otpRef.current?.clear();
        setCode("");
      }
    } finally {
      if (verifyAbortRef.current === controller) {
        verifyAbortRef.current = null;
      }
    }
  };

  const onResend = async () => {
    if (cooldown > 0 || resending || !email) return;
    void Haptics.selectionAsync();
    setResending(true);
    setError(null);
    const controller = new AbortController();
    resendAbortRef.current = controller;
    try {
      await sendCode(email, { signal: controller.signal });
      setCooldown(RESEND_COOLDOWN_SECONDS);
      otpRef.current?.clear();
      setCode("");
    } catch (err) {
      if (!controller.signal.aborted) {
        void Haptics.notificationAsync(Haptics.NotificationFeedbackType.Error);
        setError(
          mapAuthError(
            err,
            t(
              "mobile.errors.resend_failed",
              "Couldn't resend the code. Try again.",
            ),
          ),
        );
      }
    } finally {
      if (resendAbortRef.current === controller) {
        resendAbortRef.current = null;
      }
      setResending(false);
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
                {t("verify.title", "Check your email")}
              </Text>
              <Text className="text-sm text-muted-foreground text-center">
                {t("verify.description", { email })}
              </Text>
            </View>
          </View>

          <View className="gap-3 items-center">
            <OtpInput
              ref={otpRef}
              numberOfDigits={CODE_LENGTH}
              value={code}
              onChange={setCode}
              onComplete={submit}
              autoFocus
              editable={!submitting}
            />
            {error ? (
              <Text className="text-sm text-destructive">{error}</Text>
            ) : null}
          </View>

          <View className="gap-3">
            <Button
              size="lg"
              disabled={submitting || code.length < CODE_LENGTH}
              onPress={() => void submit(code)}
            >
              <Text>
                {submitting
                  ? t("mobile.verifying", "Verifying...")
                  : t("mobile.verify", "Verify")}
              </Text>
            </Button>

            <Pressable
              onPress={() => void onResend()}
              disabled={cooldown > 0 || resending}
              className="py-2 items-center"
              testID="verify-resend"
            >
              <Text
                className={
                  cooldown > 0 || resending
                    ? "text-sm text-muted-foreground"
                    : "text-sm text-primary"
                }
              >
                {resending
                  ? t("signin.sending", "Sending code...")
                  : cooldown > 0
                    ? t("verify.resend_cooldown", { seconds: cooldown })
                    : t("verify.resend", "Resend code")}
              </Text>
            </Pressable>

            <Button
              variant="ghost"
              disabled={submitting}
              onPress={() => router.back()}
            >
              <Text>{t("mobile.different_email", "Use a different email")}</Text>
            </Button>
          </View>
        </View>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}
