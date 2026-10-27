/**
 * Tasks toolbar pill-row layout decision (RUYI-344 item 10): at fontScale 1
 * the five pills keep the QA-accepted single-row horizontal scroller. Above
 * 1.0 the pills overflow the row and the scroller's drag response proved
 * dead on device (clipped pills, zero displacement — QA item 10 P2), so the
 * row switches to an adaptive wrapping flow where every TAB stays visible
 * and tappable without relying on a gesture.
 */
export const shouldWrapTaskPills = (fontScale: number): boolean =>
  fontScale > 1;
