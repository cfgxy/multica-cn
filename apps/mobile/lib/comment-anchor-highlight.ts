/**
 * 评论锚点高亮的起算闸门（RUYI-108 第三轮返工）。
 *
 * 问题：高亮原本在**点击那一刻**起算，而定位是一条异步且可能很慢的路径——
 * 折叠线程要先展开、行布局要重试、行内校正最多再走 5 × 120ms，控制器整体
 * 允许到 `LOCATE_TIMEOUT_MS`（4s）。高亮总预算只有
 * `COMMENT_HIGHLIGHT_TOTAL_MS`（1.7s），慢路径下目标最终入屏时高亮早已播完，
 * 用户看到的是「跳过去了，但没有任何东西被标出来」——直接违反验收里的
 * 「定位并高亮」。
 *
 * 修法：点击时只 `arm` 一个待高亮意图（带定位 nonce），等定位流程结束
 * （`located` 或 `failed`）再由 `settle` 交还要高亮的评论 id，由调用方此时
 * 才启动那 1.7s 的完整播放。
 *
 * 失败也放行是刻意的：定位失败不代表目标不在屏上（可能一直就在视口里，只是
 * 没拿到 viewability 确认），此时静默会让用户觉得点击没反应。两条路径都在
 * 「定位流程结束」这同一时刻起算，高亮因此总能完整播放。
 *
 * nonce 比对是防串扰的：用户连点两个不同引用时，第一次定位的迟到回调不得
 * 点亮第二次的目标。
 */

/** 已武装但尚未起算的高亮意图。 */
interface ArmedHighlight {
  commentId: string;
  /** 对应的 focus 意图 nonce（store 每次 requestFocus 递增）。 */
  nonce: number;
}

export class AnchorHighlightGate {
  private armed: ArmedHighlight | null = null;

  /** 点击引用时登记：这次定位结束后要高亮哪条评论。 */
  arm(commentId: string, nonce: number): void {
    this.armed = { commentId, nonce };
  }

  /**
   * 定位流程结束（located / failed）时调用。nonce 对得上才返回待高亮的
   * 评论 id 并消费掉该意图；对不上（已被后一次点击顶掉）返回 null。
   */
  settle(nonce: number): string | null {
    const armed = this.armed;
    if (!armed || armed.nonce !== nonce) return null;
    this.armed = null;
    return armed.commentId;
  }

  /** 丢弃待起算的意图（卸载）。 */
  disarm(): void {
    this.armed = null;
  }
}
