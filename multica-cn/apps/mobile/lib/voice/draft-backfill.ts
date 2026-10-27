/**
 * 语音口述定稿 → 草稿回填拼接（RUYI-474）。Issue 评论草稿与 quick-create
 * prompt 的回填同语义：空草稿直接落入口述内容；非空草稿先收敛尾部空白，
 * 再以空行分隔追加，多段口述互不粘连。
 */
export function appendVoiceTurnToDraft(prev: string, spoken: string): string {
  const trimmed = prev.trimEnd();
  return trimmed ? `${trimmed}\n\n${spoken}` : spoken;
}
