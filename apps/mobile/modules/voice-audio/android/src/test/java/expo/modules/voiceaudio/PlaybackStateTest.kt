package expo.modules.voiceaudio

import org.junit.Assert.assertFalse
import org.junit.Assert.assertTrue
import org.junit.Test

class PlaybackStateTest {
  @Test
  fun interruptInvalidatesCurrentAudioAndAllowsFollowingAudioInSameSession() {
    val state = PlaybackState()
    state.start()
    val interruptedChunk = state.currentGeneration()

    state.interrupt()

    assertTrue(state.isRunning())
    assertFalse(state.canWrite(interruptedChunk))

    val followingChunk = state.currentGeneration()
    assertTrue(state.canWrite(followingChunk))
  }

  @Test
  fun stopClosesPlaybackUntilStartedAgain() {
    val state = PlaybackState()
    state.start()
    val chunk = state.currentGeneration()

    state.stop()

    assertFalse(state.isRunning())
    assertFalse(state.canWrite(chunk))
  }
}
