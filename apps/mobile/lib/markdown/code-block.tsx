/**
 * Fenced code block. Wrapped in a horizontal ScrollView so long lines don't
 * wrap (preserves indentation, no soft-wrap mangling). No syntax
 * highlighting in V2.1 — that's V3+ work; lowlight is ~200KB and not
 * worth the bundle cost yet.
 */
import { ScrollView, View } from "react-native";
import { Text } from "@/components/ui/text";
import {
  CODE_BLOCK_CONTAINER_CLASS,
  CODE_BLOCK_LANG_LABEL_CLASS,
  CODE_BLOCK_TEXT_CLASS,
} from "./tokens";

interface Props {
  code: string;
  lang?: string;
}

export function CodeBlock({ code, lang }: Props) {
  return (
    <View className={CODE_BLOCK_CONTAINER_CLASS}>
      {lang ? (
        <Text className={CODE_BLOCK_LANG_LABEL_CLASS}>{lang}</Text>
      ) : null}
      <ScrollView horizontal showsHorizontalScrollIndicator={false}>
        {/* selectable lets the user long-press to copy the code. iOS shows
            a single "Copy" action (the whole block); Android offers a full
            range selection. RN has no granular selection on iOS without
            switching to TextInput, which is overkill for a code viewer. */}
        <Text className={CODE_BLOCK_TEXT_CLASS} selectable>
          {code}
        </Text>
      </ScrollView>
    </View>
  );
}
