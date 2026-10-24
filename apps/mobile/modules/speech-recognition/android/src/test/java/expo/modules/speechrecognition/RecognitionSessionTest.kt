package expo.modules.speechrecognition

import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

class RecognitionSessionTest {
  @Test
  fun priorStopCannotOperateOnReplacementRecognizer() {
    val session = RecognitionSession<FakeRecognizer>()
    val first = FakeRecognizer()
    val replacement = FakeRecognizer()

    assertNull(session.replace(1, first))
    session.recognizerForStop(1)?.stopListening()
    assertEquals(1, first.stopCalls)
    session.replace(2, replacement)?.destroy()

    assertNull(session.recognizerForStop(1))
    assertEquals(0, replacement.stopCalls)
    session.recognizerForStop(2)?.stopListening()
    assertEquals(1, replacement.stopCalls)
  }

  @Test
  fun terminalCallbackReleasesRecognizerBeforeEventCanReenter() {
    val session = RecognitionSession<FakeRecognizer>()
    val recognizer = FakeRecognizer()
    session.replace(1, recognizer)

    session.takeIfCurrent(recognizer)?.destroy()

    assertNull(session.recognizerForStop(1))
    assertNull(session.takeIfCurrent(recognizer))
    assertEquals(1, recognizer.destroyCalls)
  }

  @Test
  fun lateCallbackCannotClaimReplacedRecognizer() {
    val session = RecognitionSession<FakeRecognizer>()
    val first = FakeRecognizer()
    val replacement = FakeRecognizer()
    session.replace(1, first)
    session.replace(2, replacement)

    assertNull(session.takeIfCurrent(first))
    assertTrue(session.isCurrent(replacement))
    session.takeIfCurrent(replacement)?.destroy()
    assertEquals(0, first.destroyCalls)
    assertEquals(1, replacement.destroyCalls)
  }

  @Test
  fun moduleTeardownClearsRecognizerBeforeLateCallback() {
    val session = RecognitionSession<FakeRecognizer>()
    val recognizer = FakeRecognizer()
    session.replace(1, recognizer)

    session.takeCurrent()?.destroy()

    assertNull(session.recognizerForStop(1))
    assertNull(session.takeIfCurrent(recognizer))
    assertEquals(1, recognizer.destroyCalls)
  }

  private class FakeRecognizer {
    var destroyCalls = 0
    var stopCalls = 0

    fun destroy() {
      destroyCalls += 1
    }

    fun stopListening() {
      stopCalls += 1
    }
  }
}
