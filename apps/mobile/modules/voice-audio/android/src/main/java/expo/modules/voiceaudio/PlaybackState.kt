package expo.modules.voiceaudio

import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

internal class PlaybackState {
  private val running = AtomicBoolean(false)
  private val generation = AtomicLong(0)

  fun start() {
    generation.incrementAndGet()
    running.set(true)
  }

  fun currentGeneration(): Long = generation.get()

  fun canWrite(expectedGeneration: Long): Boolean =
    running.get() && generation.get() == expectedGeneration

  fun isRunning(): Boolean = running.get()

  fun interrupt() {
    generation.incrementAndGet()
  }

  fun stop() {
    running.set(false)
    generation.incrementAndGet()
  }
}
