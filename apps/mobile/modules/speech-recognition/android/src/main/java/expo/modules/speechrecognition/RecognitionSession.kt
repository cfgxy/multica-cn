package expo.modules.speechrecognition

/**
 * Tracks the recognizer that is allowed to receive commands for one native
 * speech session. Access is confined to the Android main thread.
 */
internal class RecognitionSession<T : Any> {
  private data class Active<T : Any>(
    val id: Long,
    val recognizer: T,
  )

  private var active: Active<T>? = null

  /** Replaces the active recognizer and returns the instance that must be destroyed. */
  fun replace(id: Long, recognizer: T): T? {
    val replaced = active?.recognizer
    active = Active(id, recognizer)
    return replaced
  }

  /** Returns the active recognizer only when a stop belongs to its session. */
  fun recognizerForStop(id: Long): T? = active?.takeIf { it.id == id }?.recognizer

  /** Atomically removes a terminal recognizer before it can dispatch an event. */
  fun takeIfCurrent(recognizer: T): T? {
    val current = active ?: return null
    if (current.recognizer !== recognizer) return null
    active = null
    return current.recognizer
  }

  /** Removes the active recognizer during replacement or module teardown. */
  fun takeCurrent(): T? {
    val current = active?.recognizer
    active = null
    return current
  }

  fun isCurrent(recognizer: T): Boolean = active?.recognizer === recognizer
}
