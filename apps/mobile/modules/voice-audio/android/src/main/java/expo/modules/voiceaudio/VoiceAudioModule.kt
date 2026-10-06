package expo.modules.voiceaudio

import android.Manifest
import android.annotation.SuppressLint
import android.media.AudioAttributes
import android.media.AudioFormat
import android.media.AudioManager
import android.media.AudioRecord
import android.media.AudioTrack
import android.media.MediaRecorder
import android.util.Base64
import android.content.pm.PackageManager
import androidx.core.content.ContextCompat
import expo.modules.kotlin.Promise
import expo.modules.kotlin.exception.CodedException
import expo.modules.interfaces.permissions.PermissionsStatus
import expo.modules.kotlin.modules.Module
import expo.modules.kotlin.modules.ModuleDefinition
import java.util.concurrent.ExecutorService
import java.util.concurrent.Executors
import java.util.concurrent.atomic.AtomicBoolean

/**
 * Android 语音采集/播放入口（RUYI-449）。Android-only（与 modules/share-intent
 * 同模式），iOS 未实现，JS 侧按 Platform 收敛入口。
 *
 * 职责边界：只做「麦克风 PCM 流 → base64 块事件」与「base64 块 → 扬声器」。
 * 会话状态机、鉴权帧、线格式全部在 JS 侧（lib/voice/ + @multica/core/voice），
 * Kotlin 保持薄壳。
 *
 * 采集：AudioRecord 单声道 16 kHz PCM16，VOICE_COMMUNICATION 音源（系统级
 * AEC/NS/AGC，与 Web 端 getUserMedia 的 echoCancellation/noiseSuppression/
 * autoGainControl 语义对齐），读循环每 ~100 ms（3200 字节）发一次
 * onAudioChunk。stop 置位后由读线程自行退出并释放，双保险防泄漏。
 *
 * 播放：AudioTrack STREAM 模式，24 kHz 单声道 PCM16（网关下行速率），块
 * 顺序写入由单线程 executor 保证；interrupt 全清并重建 track。
 *
 * 安全：日志只记异常类名；音频数据只经事件桥向 JS 传递，不落盘、不打日志。
 */
class VoiceAudioModule : Module() {
  companion object {
    private const val TAG = "VoiceAudioModule"
    private const val CAPTURE_RATE = 16000
    private const val PLAYBACK_RATE = 24000
    /** 100 ms of 16 kHz mono PCM16. */
    private const val CHUNK_BYTES = CAPTURE_RATE * 2 / 10
    private const val REQUEST_PERMISSIONS_CODE = 47011
  }

  private val captureRunning = AtomicBoolean(false)
  private var audioRecord: AudioRecord? = null
  private var captureThread: Thread? = null

  private val playbackExecutor: ExecutorService = Executors.newSingleThreadExecutor()
  private val playbackRunning = AtomicBoolean(false)
  private var audioTrack: AudioTrack? = null

  override fun definition() = ModuleDefinition {
    Name("VoiceAudio")
    Events("onAudioChunk", "onCaptureError")

    AsyncFunction("requestPermissionsAsync") { promise: Promise ->
      val granted = isRecordAudioGranted()
      if (granted) {
        promise.resolve(makeResult(true))
        return@AsyncFunction
      }
      val permissions = appContext.permissions
      if (permissions == null) {
        promise.reject(
          CodedException("E_PERMISSIONS_UNAVAILABLE", "permissions module unavailable", null),
        )
        return@AsyncFunction
      }
      permissions.askForPermissions(
        { result ->
          // 本版 expo-modules-core 的监听器给的是 Map<权限名, PermissionsResponse>。
          val response = result[Manifest.permission.RECORD_AUDIO]
          promise.resolve(
            makeResult(response?.status == PermissionsStatus.GRANTED),
          )
        },
        Manifest.permission.RECORD_AUDIO,
      )
    }

    AsyncFunction("start") { promise: Promise ->
      if (captureRunning.get()) {
        promise.resolve(null)
        return@AsyncFunction
      }
      if (!isRecordAudioGranted()) {
        promise.reject(CodedException("E_PERMISSION_DENIED", "RECORD_AUDIO not granted", null))
        return@AsyncFunction
      }
      try {
        startCapture()
        promise.resolve(null)
      } catch (e: Exception) {
        promise.reject(CodedException("E_START_FAILED", e.message ?: e.javaClass.simpleName, null))
      }
    }

    AsyncFunction("stop") { promise: Promise ->
      stopCapture()
      promise.resolve(null)
    }

    AsyncFunction("startPlayback") { promise: Promise ->
      try {
        ensurePlaybackTrack()
        promise.resolve(null)
      } catch (e: Exception) {
        promise.reject(CodedException("E_PLAYBACK_FAILED", e.message ?: e.javaClass.simpleName, null))
      }
    }

    AsyncFunction("playChunk") { base64: String, promise: Promise ->
      val executor = playbackExecutor
      if (!playbackRunning.get()) {
        promise.resolve(null)
        return@AsyncFunction
      }
      executor.execute {
        val track = audioTrack ?: return@execute
        try {
          val pcm = Base64.decode(base64, Base64.NO_WRAP)
          var offset = 0
          while (offset < pcm.size && playbackRunning.get()) {
            val written = track.write(pcm, offset, pcm.size - offset, AudioTrack.WRITE_BLOCKING)
            if (written <= 0) break
            offset += written
          }
          promise.resolve(null)
        } catch (e: Exception) {
          sendErrorEvent(e)
          promise.reject(CodedException("E_PLAYBACK_FAILED", e.message ?: e.javaClass.simpleName, null))
        }
      }
    }

    AsyncFunction("interruptPlayback") { promise: Promise ->
      stopPlaybackInternal()
      promise.resolve(null)
    }

    AsyncFunction("stopPlayback") { promise: Promise ->
      stopPlaybackInternal()
      promise.resolve(null)
    }

    OnDestroy {
      stopCapture()
      stopPlaybackInternal()
      playbackExecutor.shutdown()
    }
  }

  private fun makeResult(granted: Boolean): Map<String, Any?> = mapOf(
    "granted" to granted,
  )

  private fun isRecordAudioGranted(): Boolean {
    val context = appContext.reactContext ?: return false
    return ContextCompat.checkSelfPermission(context, Manifest.permission.RECORD_AUDIO) ==
      PackageManager.PERMISSION_GRANTED
  }

  @SuppressLint("MissingPermission")
  private fun startCapture() {
    val minBuf = AudioRecord.getMinBufferSize(
      CAPTURE_RATE,
      AudioFormat.CHANNEL_IN_MONO,
      AudioFormat.ENCODING_PCM_16BIT,
    )
    if (minBuf <= 0) {
      throw IllegalStateException("AudioRecord.getMinBufferSize failed: $minBuf")
    }
    val record = AudioRecord(
      MediaRecorder.AudioSource.VOICE_COMMUNICATION,
      CAPTURE_RATE,
      AudioFormat.CHANNEL_IN_MONO,
      AudioFormat.ENCODING_PCM_16BIT,
      maxOf(minBuf * 2, CHUNK_BYTES * 4),
    )
    if (record.state != AudioRecord.STATE_INITIALIZED) {
      record.release()
      throw IllegalStateException("AudioRecord init failed")
    }
    audioRecord = record
    captureRunning.set(true)
    record.startRecording()
    val thread = Thread({
      val buffer = ByteArray(CHUNK_BYTES)
      try {
        while (captureRunning.get()) {
          val read = audioRecord?.read(buffer, 0, buffer.size) ?: -1
          if (read <= 0) continue
          val b64 = Base64.encodeToString(buffer, 0, read, Base64.NO_WRAP)
          sendEvent("onAudioChunk", mapOf("data" to b64))
        }
      } catch (e: Exception) {
        if (captureRunning.get()) {
          sendErrorEvent(e)
        }
      } finally {
        try {
          audioRecord?.stop()
        } catch (_: IllegalStateException) {
        }
        audioRecord?.release()
        audioRecord = null
      }
    }, "voice-audio-capture")
    thread.priority = Thread.MAX_PRIORITY
    thread.start()
    captureThread = thread
  }

  private fun stopCapture() {
    if (!captureRunning.compareAndSet(true, false)) return
    try {
      captureThread?.join(1000)
    } catch (_: InterruptedException) {
      Thread.currentThread().interrupt()
    }
    captureThread = null
  }

  private fun ensurePlaybackTrack() {
    stopPlaybackInternal()
    val minBuf = AudioTrack.getMinBufferSize(
      PLAYBACK_RATE,
      AudioFormat.CHANNEL_OUT_MONO,
      AudioFormat.ENCODING_PCM_16BIT,
    )
    if (minBuf <= 0) {
      throw IllegalStateException("AudioTrack.getMinBufferSize failed: $minBuf")
    }
    val track = AudioTrack(
      AudioAttributes.Builder()
        .setUsage(AudioAttributes.USAGE_MEDIA)
        .setContentType(AudioAttributes.CONTENT_TYPE_SPEECH)
        .build(),
      AudioFormat.Builder()
        .setSampleRate(PLAYBACK_RATE)
        .setEncoding(AudioFormat.ENCODING_PCM_16BIT)
        .setChannelMask(AudioFormat.CHANNEL_OUT_MONO)
        .build(),
      maxOf(minBuf * 4, CHUNK_BYTES * 8),
      AudioTrack.MODE_STREAM,
      AudioManager.AUDIO_SESSION_ID_GENERATE,
    )
    track.play()
    audioTrack = track
    playbackRunning.set(true)
  }

  private fun stopPlaybackInternal() {
    playbackRunning.set(false)
    val track = audioTrack
    audioTrack = null
    if (track != null) {
      try {
        track.pause()
        track.flush()
        track.stop()
      } catch (_: IllegalStateException) {
      }
      track.release()
    }
  }

  private fun sendErrorEvent(e: Exception) {
    sendEvent("onCaptureError", mapOf("message" to e.javaClass.simpleName))
  }
}

