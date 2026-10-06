package expo.modules.shareintent

import android.content.Intent
import android.net.Uri
import android.provider.OpenableColumns
import android.util.Log
import androidx.core.content.IntentCompat
import expo.modules.kotlin.Promise
import expo.modules.kotlin.modules.Module
import expo.modules.kotlin.modules.ModuleDefinition
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch
import java.io.File
import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong
import java.util.concurrent.atomic.AtomicReference

/**
 * Android Share Intent 入口（RUYI-463）。
 *
 * 职责边界：只做「读取 intent → 把流复制进本应用 cache → 报告文件清单」。
 * 解析校验、落地选型、附件注入全部在 JS 侧（lib/share-payload.ts +
 * data/stores/shared-intent-store.ts），Kotlin 保持薄壳，行为由 AVD 实测
 * 覆盖。
 *
 * 两条入口路径：
 * - 冷启：MainActivity 由分享 intent 创建，getInitialShare() 读
 *   activity.intent。AsyncFunction 在后台线程执行，复制大文件不阻塞 UI。
 * - 热启：singleTask 栈顶复用，OnNewIntent 触发，复制完成后发
 *   onShareIntent 事件。
 *
 * 桥接竞态：JS 订阅事件之前到达的分享会丢事件（expo 事件不缓冲），
 * 因此每次提取的载荷都带自增 id 存入单槽 buffer；getInitialShare 同时
 * 返回「启动 intent 载荷」或「未消费的 buffer」。JS 侧 navigator 对
 * 两路来源按 id 去重，双投递无副作用。
 *
 * 安全：文件名清洗路径分隔符与控制字符后再落盘；复制失败逐文件跳过；
 * 日志只记异常类名，永不包含文件内容与 URI 查询参数。
 */
class ShareIntentModule : Module() {
  companion object {
    private const val TAG = "ShareIntentModule"
    private const val CACHE_SUBDIR = "share-intent"
    private const val MAX_FILES = 20
    private const val MAX_NAME_LENGTH = 200
    private const val FALLBACK_NAME = "shared-file"
  }

  private val ioScope = CoroutineScope(Dispatchers.IO)
  /** 启动 intent 的载荷只交付一次：登出/登入导致 navigator 重挂时不再重放。 */
  private val initialConsumed = AtomicBoolean(false)
  private val idCounter = AtomicLong(0)
  /** onShareIntent 事件先于 JS 订阅到达时的唯一缓冲槽。 */
  private val bufferedPayload = AtomicReference<Map<String, Any?>?>(null)

  override fun definition() = ModuleDefinition {
    Name("ShareIntent")
    Events("onShareIntent")

    AsyncFunction("getInitialShare") { promise: Promise ->
      val launchPayload = extractFromLaunchIntentOnce()
      val buffered = bufferedPayload.getAndSet(null)
      val payload = launchPayload ?: buffered
      if (payload == null) {
        promise.resolve(null)
      } else {
        // payload 已就绪（复制在后台线程完成后才进入本函数），直接交付。
        promise.resolve(payload)
      }
    }

    OnNewIntent { intent ->
      ioScope.launch {
        val payload = extractShareFiles(intent) ?: return@launch
        bufferedPayload.set(payload)
        sendEvent("onShareIntent", payload)
      }
    }
  }

  private fun extractFromLaunchIntentOnce(): Map<String, Any?>? {
    if (initialConsumed.getAndSet(true)) return null
    val activity = appContext?.currentActivity ?: return null
    return extractShareFiles(activity.intent)
  }

  /** 无流负载（纯文本分享等）返回 null：不进落地页，不崩溃。 */
  private fun extractShareFiles(intent: Intent?): Map<String, Any?>? {
    if (intent == null) return null
    val uris: List<Uri> = when (intent.action) {
      Intent.ACTION_SEND ->
        IntentCompat.getParcelableExtra(intent, Intent.EXTRA_STREAM, Uri::class.java)
          ?.let { listOf(it) } ?: return null
      Intent.ACTION_SEND_MULTIPLE ->
        IntentCompat.getParcelableArrayListExtra(intent, Intent.EXTRA_STREAM, Uri::class.java)
          ?: return null
      else -> return null
    }

    val context = appContext?.reactContext ?: return null
    val resolver = context.contentResolver
    val fallbackType = intent.type
    val outDir = File(context.cacheDir, CACHE_SUBDIR).apply { mkdirs() }

    val files = ArrayList<Map<String, Any?>>()
    for (uri in uris.take(MAX_FILES)) {
      try {
        val name = queryDisplayName(resolver, uri)
          ?: "$FALLBACK_NAME-${idCounter.incrementAndGet()}"
        val safeName = sanitizeFileName(name, idCounter.incrementAndGet())
        val dest = uniqueDestination(File(outDir, safeName))
        resolver.openInputStream(uri)?.use { input ->
          dest.outputStream().use { output -> input.copyTo(output) }
        } ?: continue
        val mimeType = resolver.getType(uri) ?: fallbackType ?: "application/octet-stream"
        files.add(
          mapOf(
            "uri" to Uri.fromFile(dest).toString(),
            "name" to dest.name,
            "mimeType" to mimeType,
            "size" to dest.length(),
          ),
        )
      } catch (e: Exception) {
        // 复制失败逐文件跳过；不记 URI（可能带查询参数）、不记内容。
        Log.w(TAG, "share file copy failed: ${e.javaClass.simpleName}")
      }
    }
    if (files.isEmpty()) return null
    return mapOf(
      "id" to "${System.currentTimeMillis()}-${idCounter.incrementAndGet()}",
      "files" to files,
    )
  }

  private fun queryDisplayName(
    resolver: android.content.ContentResolver,
    uri: Uri,
  ): String? {
    resolver
      .query(uri, arrayOf(OpenableColumns.DISPLAY_NAME), null, null, null)
      ?.use { cursor ->
        val idx = cursor.getColumnIndex(OpenableColumns.DISPLAY_NAME)
        if (idx >= 0 && cursor.moveToFirst()) return cursor.getString(idx)
      }
    return null
  }

  /** 路径分隔符 / Windows 保留字符 / 控制字符一律替换；超长保留尾部以
   *  存活扩展名；重名按序号去重（EXTRA_STREAM 里同名文件并存）。 */
  private fun sanitizeFileName(raw: String, salt: Long): String {
    val cleaned = raw.replace(Regex("[\\\\/:*?\"<>|\u0000-\u001f]"), "_").trim()
    val base = when {
      cleaned.isEmpty() -> "$FALLBACK_NAME-$salt"
      cleaned.length <= MAX_NAME_LENGTH -> cleaned
      else -> cleaned.substring(cleaned.length - MAX_NAME_LENGTH)
    }
    return base
  }

  private fun uniqueDestination(candidate: File): File {
    if (!candidate.exists()) return candidate
    val dot = candidate.name.lastIndexOf('.')
    val stem = if (dot > 0) candidate.name.substring(0, dot) else candidate.name
    val ext = if (dot > 0) candidate.name.substring(dot) else ""
    var index = 1
    while (true) {
      val next = File(candidate.parentFile, "$stem-$index$ext")
      if (!next.exists()) return next
      index++
    }
  }
}
