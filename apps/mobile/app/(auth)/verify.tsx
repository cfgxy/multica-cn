import { useState } from "react";
import { KeyboardAvoidingView, Platform, View } from "react-native";
import { SafeAreaView } from "react-native-safe-area-context";
import { router, useLocalSearchParams } from "expo-router";
import { Text } from "@/components/ui/text";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";
import { useAuthStore } from "@/data/auth-store";

export default function Verify() {
  const verifyCode = useAuthStore((s) => s.verifyCode);
  const { email = "" } = useLocalSearchParams<{ email?: string }>();
  const [code, setCode] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const onSubmit = async () => {
    const trimmed = code.trim();
    if (!trimmed || !email) return;
    setSubmitting(true);
    setError(null);
    try {
      await verifyCode(email, trimmed);
      // Successful verify: route to the entry redirect, which decides where
      // to go based on auth + persisted workspace slug.
      router.replace("/");
    } catch (err) {
      setError(err instanceof Error ? err.message : "Invalid code");
      setSubmitting(false);
    }
  };

  return (
    <SafeAreaView className="flex-1 bg-background">
      <KeyboardAvoidingView
        className="flex-1"
        behavior={Platform.OS === "ios" ? "padding" : undefined}
      >
        <View className="flex-1 justify-center px-6 gap-6">
          <View className="gap-2">
            <Text className="text-3xl font-bold text-foreground">
              Enter verification code
            </Text>
            <Text className="text-base text-muted-foreground">
              We sent a code to {email}
            </Text>
          </View>

          <View className="gap-3">
            <Input
              autoCapitalize="none"
              keyboardType="number-pad"
              placeholder="6-digit code"
              value={code}
              onChangeText={setCode}
              onSubmitEditing={onSubmit}
              returnKeyType="go"
              editable={!submitting}
              maxLength={8}
            />
            {error ? (
              <Text className="text-sm text-destructive">{error}</Text>
            ) : null}
          </View>

          <View className="gap-3">
            <Button
              disabled={submitting || !code.trim()}
              onPress={onSubmit}
            >
              {submitting ? "Verifying..." : "Verify"}
            </Button>
            <Button
              variant="outline"
              disabled={submitting}
              onPress={() => router.back()}
            >
              Back
            </Button>
          </View>
        </View>
      </KeyboardAvoidingView>
    </SafeAreaView>
  );
}
