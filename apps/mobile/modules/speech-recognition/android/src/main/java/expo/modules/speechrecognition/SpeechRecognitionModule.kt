package expo.modules.speechrecognition

import android.Manifest
import android.content.pm.PackageManager
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.speech.RecognitionListener
import android.speech.RecognizerIntent
import android.speech.SpeechRecognizer
import androidx.core.content.ContextCompat
import expo.modules.kotlin.Promise
import expo.modules.kotlin.exception.CodedException
import expo.modules.kotlin.modules.Module
import expo.modules.kotlin.modules.ModuleDefinition
import java.util.Locale

/**
 * Android 端侧语音识别入口（RUYI-694 D3 听写链）。Android-only，与
 * modules/voice-audio 同模式；iOS 未实现，JS 侧按 Platform 收敛入口。
 *
 * 职责边界：只做「按住说话 → onPartialResult/onFinalResult 事件」的薄壳。
 * 听写五态状态机、发送语义、降级判定全在 JS 侧（lib/voice/dictation.ts）。
 *
 * 端面：`SpeechRecognizer.createOnDeviceSpeechRecognizer`（API 31+，音频
 * 不出设备，隐私语义与设计稿一致）。API < 31 或设备无 on-device 识别模块
 * 时 `isOnDeviceAvailableAsync` 返回 false，JS 侧据此收敛入口；本模块
 * 不提供联网识别路径（该降级方向由服务端转写端点承载，另行裁定）。
 *
 * 事件：onPartialResult{ text }（实时预览）、onFinalResult{ text }（松开
 * 后终稿，可能为空串）、onError{ code }（SpeechRecognizer.ERROR_* 原值）、
 * onRms{ value }（响度 0..1 归一，驱动波形动画；SpeechRecognizer 持麦，
 * 复用 voice-audio 的 PCM 流不可行）。
 *
 * 线程：SpeechRecognizer 要求主线程调用，AsyncFunction 一律 post 到主
 * 线程执行；识别器用完即毁（每次听写新建，避免会话状态残留）。
 *
 * 安全：日志只记异常类名；识别文本只经事件桥向 JS 传递，不落盘。
 */
class SpeechRecognitionModule : Module() {
  companion object {
    private const val TAG = "SpeechRecognitionModule"
    private const val REQUEST_PERMISSIONS_CODE = 47012
  }

  private val mainHandler = Handler(Looper.getMainLooper())
  private var recognizer: SpeechRecognizer? = null

  override fun definition() = ModuleDefinition {
    Name("SpeechRecognition")
    Events("onPartialResult", "onFinalResult", "onError", "onRms")

    AsyncFunction("isOnDeviceAvailableAsync") { promise: Promise ->
      val context = appContext.reactContext
      if (context == null) {
        promise.reject(CodedException("E_CONTEXT_UNAVAILABLE", "react context unavailable", null))
        return@AsyncFunction
      }
      val granted = isRecordAudioGranted()
      if (!granted) {
        // 无麦克风权限时识别不可用：调用方先走权限请求，这里如实上报。
        promise.resolve(mapOf("available" to false, "reason" to "permission"))
        return@AsyncFunction
      }
      if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S) {
        promise.resolve(mapOf("available" to false, "reason" to "api_level"))
        return@AsyncFunction
      }
      val available = try {
        SpeechRecognizer.isOnDeviceRecognitionAvailable(context)
      } catch (t: Throwable) {
        android.util.Log.w(TAG, t.javaClass.simpleName)
        false
      }
      promise.resolve(mapOf("available" to available, "reason" to if (available) "ok" else "module_missing"))
    }

    AsyncFunction("requestPermissionsAsync") { promise: Promise ->
      val granted = isRecordAudioGranted()
      if (granted) {
        promise.resolve(mapOf("granted" to true))
        return@AsyncFunction
      }
      val permissions = appContext.permissions
      if (permissions == null) {
        promise.reject(CodedException("E_PERMISSIONS_UNAVAILABLE", "permissions module unavailable", null))
        return@AsyncFunction
      }
      permissions.askForPermissions(
        { result ->
          val response = result[Manifest.permission.RECORD_AUDIO]
          promise.resolve(mapOf("granted" to (response?.status == expo.modules.interfaces.permissions.PermissionsStatus.GRANTED)))
        },
        // 本版 expo-modules-core 的 askForPermissions 第二实参为单权限 String（同 voice-audio 用法）。
        Manifest.permission.RECORD_AUDIO,
      )
    }

    AsyncFunction("start") { promise: Promise ->
      val context = appContext.reactContext
      if (context == null) {
        promise.reject(CodedException("E_CONTEXT_UNAVAILABLE", "react context unavailable", null))
        return@AsyncFunction
      }
      if (Build.VERSION.SDK_INT < Build.VERSION_CODES.S ||
        !SpeechRecognizer.isOnDeviceRecognitionAvailable(context)
      ) {
        promise.reject(CodedException("E_ON_DEVICE_UNAVAILABLE", "on-device recognition unavailable", null))
        return@AsyncFunction
      }
      mainHandler.post {
        try {
          destroyRecognizer()
          val rec = SpeechRecognizer.createOnDeviceSpeechRecognizer(context)
          recognizer = rec
          rec.setRecognitionListener(listener)
          val intent = android.content.Intent(RecognizerIntent.ACTION_RECOGNIZE_SPEECH).apply {
            putExtra(RecognizerIntent.EXTRA_LANGUAGE_MODEL, RecognizerIntent.LANGUAGE_MODEL_FREE_FORM)
            putExtra(RecognizerIntent.EXTRA_PARTIAL_RESULTS, true)
            putExtra(RecognizerIntent.EXTRA_MAX_RESULTS, 1)
            // 听写跟随系统语言：用户用自己的语言说话，不随 App UI 语言切换。
            putExtra(RecognizerIntent.EXTRA_LANGUAGE, Locale.getDefault().toLanguageTag())
          }
          rec.startListening(intent)
          promise.resolve(null)
        } catch (t: Throwable) {
          android.util.Log.w(TAG, t.javaClass.simpleName)
          destroyRecognizer()
          promise.reject(CodedException("E_START_FAILED", t.javaClass.simpleName, t))
        }
      }
    }

    AsyncFunction("stop") { promise: Promise ->
      mainHandler.post {
        try {
          recognizer?.stopListening()
          promise.resolve(null)
        } catch (t: Throwable) {
          android.util.Log.w(TAG, t.javaClass.simpleName)
          promise.reject(CodedException("E_STOP_FAILED", t.javaClass.simpleName, t))
        }
      }
    }

    OnDestroy { destroyRecognizer() }
  }

  private fun isRecordAudioGranted(): Boolean {
    val context = appContext.reactContext ?: return false
    return ContextCompat.checkSelfPermission(context, Manifest.permission.RECORD_AUDIO) ==
      PackageManager.PERMISSION_GRANTED
  }

  /** 用完即毁：释放识别器并清引用，主线程执行。 */
  private fun destroyRecognizer() {
    if (Looper.myLooper() == Looper.getMainLooper()) {
      recognizer?.destroy()
      recognizer = null
    } else {
      mainHandler.post {
        recognizer?.destroy()
        recognizer = null
      }
    }
  }

  private fun firstText(results: Bundle?): String? {
    val list = results?.getStringArrayList(SpeechRecognizer.RESULTS_RECOGNITION) ?: return null
    return list.firstOrNull()
  }

  private val listener = object : RecognitionListener {
    override fun onReadyForSpeech(params: Bundle?) {}
    override fun onBeginningOfSpeech() {}

    override fun onRmsChanged(rmsdB: Float) {
      // 静音 ≈ -2 dB，正常说话 0..10+；夹到 0..1 驱动波形条。
      val normalized = ((rmsdB + 2f) / 12f).coerceIn(0f, 1f)
      sendEvent("onRms", mapOf("value" to normalized))
    }

    override fun onBufferReceived(buffer: ByteArray?) {}
    override fun onEndOfSpeech() {}

    override fun onError(error: Int) {
      sendEvent("onError", mapOf("code" to error))
      destroyRecognizer()
    }

    override fun onResults(results: Bundle?) {
      sendEvent("onFinalResult", mapOf("text" to (firstText(results) ?: "")))
      destroyRecognizer()
    }

    override fun onPartialResults(partialResults: Bundle?) {
      val text = firstText(partialResults) ?: return
      sendEvent("onPartialResult", mapOf("text" to text))
    }

    override fun onEvent(eventType: Int, params: Bundle?) {}
  }
}
