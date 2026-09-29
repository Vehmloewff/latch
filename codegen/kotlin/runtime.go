package kotlin

// runtimeSource is embedded verbatim in the generated file. It requires only
// the JDK (java.net.http WebSocket, Java 11+) and Kotlin stdlib.
const runtimeSource = `import java.io.ByteArrayOutputStream
import java.net.URI
import java.net.http.HttpClient
import java.net.http.WebSocket
import java.nio.ByteBuffer
import java.nio.ByteOrder
import java.nio.charset.CodingErrorAction
import java.nio.charset.StandardCharsets
import java.time.Instant
import java.util.concurrent.CompletableFuture

import java.util.concurrent.atomic.AtomicBoolean
import java.util.concurrent.atomic.AtomicLong

class LatchError(val code: String, message: String) : RuntimeException(message)

internal object LatchValue {
  data class Unsigned(val value: ULong)
  data class Single(val value: Float)
  data class Time(val value: Instant)
  data class Structure(val fields: Map<Long, Any?>)

  private fun bad(): Nothing = throw LatchError("malformed_value", "unexpected binary value")
  fun required(fields: Map<Long, Any?>, id: Long): Any? = if (fields.containsKey(id)) fields[id] else bad()
  fun string(v: Any?): String = v as? String ?: bad()
  fun boolean(v: Any?): Boolean = v as? Boolean ?: bad()
  fun signed(v: Any?): Long = v as? Long ?: bad()
  fun int8(v: Any?): Byte = signed(v).let { if (it in Byte.MIN_VALUE.toLong()..Byte.MAX_VALUE.toLong()) it.toByte() else bad() }
  fun int16(v: Any?): Short = signed(v).let { if (it in Short.MIN_VALUE.toLong()..Short.MAX_VALUE.toLong()) it.toShort() else bad() }
  fun int32(v: Any?): Int = signed(v).let { if (it in Int.MIN_VALUE.toLong()..Int.MAX_VALUE.toLong()) it.toInt() else bad() }
  fun unsigned(v: Any?): ULong = (v as? Unsigned)?.value ?: bad()
  fun uint8(v: Any?): UByte = unsigned(v).let { if (it <= UByte.MAX_VALUE.toULong()) it.toUByte() else bad() }
  fun uint16(v: Any?): UShort = unsigned(v).let { if (it <= UShort.MAX_VALUE.toULong()) it.toUShort() else bad() }
  fun uint32(v: Any?): UInt = unsigned(v).let { if (it <= UInt.MAX_VALUE.toULong()) it.toUInt() else bad() }
  fun single(v: Any?): Float = (v as? Single)?.value ?: bad()
  fun double(v: Any?): Double = v as? Double ?: bad()
  fun time(v: Any?): Instant = (v as? Time)?.value ?: bad()
  fun bytes(v: Any?): ByteArray = v as? ByteArray ?: bad()
  fun structure(v: Any?): Map<Long, Any?> = (v as? Structure)?.fields ?: bad()
  @Suppress("UNCHECKED_CAST")
  fun list(v: Any?): List<Any?> = v as? List<Any?> ?: bad()
  fun array(v: Any?, size: Int): List<Any?> = list(v).also { if (it.size != size) bad() }
  fun <T> arrayInput(v: List<T>, size: Int): List<T> = v.also { if (it.size != size) bad() }
  @Suppress("UNCHECKED_CAST")
  fun map(v: Any?): Map<String, Any?> = v as? Map<String, Any?> ?: bad()
}

internal object LatchBinary {
  private fun malformed(): Nothing = throw LatchError("malformed_frame", "malformed binary frame")
  private const val MAX_CONTAINER = 1 shl 24
  class Writer {
    private val out = ByteArrayOutputStream()
    fun byte(v: Int) { out.write(v) }
    fun raw(v: ByteArray) { out.write(v) }
    fun uint(v: ULong) {
      var n = v
      do { val part = (n and 127uL).toInt(); n = n shr 7; byte(part or if (n != 0uL) 128 else 0) } while (n != 0uL)
    }
    fun signed(v: Long) { uint(((v shl 1) xor (v shr 63)).toULong()) }
    fun blob(v: ByteArray) { uint(v.size.toULong()); raw(v) }
    fun text(v: String) { val bytes = StandardCharsets.UTF_8.newEncoder().onMalformedInput(CodingErrorAction.REPORT).encode(java.nio.CharBuffer.wrap(v)); rawVarBlob(bytes) }
        private fun rawVarBlob(bytes: ByteBuffer) { uint(bytes.remaining().toULong()); val data = ByteArray(bytes.remaining()); bytes.get(data); raw(data) }
    fun result(): ByteArray = out.toByteArray()
    fun value(v: Any?, depth: Int = 0) {
      if (depth > 128) malformed()
      when (v) {
        null -> byte(0)
        false -> byte(1)
        true -> byte(2)
        is Long -> { byte(3); signed(v) }
        is LatchValue.Unsigned -> { byte(4); uint(v.value) }
        is LatchValue.Single -> { byte(5); raw(ByteBuffer.allocate(4).order(ByteOrder.LITTLE_ENDIAN).putInt(java.lang.Float.floatToRawIntBits(v.value)).array()) }
        is Double -> { byte(6); raw(ByteBuffer.allocate(8).order(ByteOrder.LITTLE_ENDIAN).putLong(java.lang.Double.doubleToRawLongBits(v)).array()) }
        is String -> { byte(7); text(v) }
        is ByteArray -> { byte(8); blob(v) }
        is LatchValue.Structure -> { byte(9); uint(v.fields.size.toULong()); v.fields.toSortedMap().forEach { (id, item) -> if (id <= 0 || id > UInt.MAX_VALUE.toLong()) malformed(); uint(id.toULong()); value(item, depth + 1) } }
        is List<*> -> { byte(10); uint(v.size.toULong()); v.forEach { value(it, depth + 1) } }
        is Map<*, *> -> { byte(11); uint(v.size.toULong()); v.keys.map { it as? String ?: malformed() }.sorted().forEach { key -> value(key, depth + 1); value(v[key], depth + 1) } }
        is LatchValue.Time -> {
          byte(12)
          val nanos = try { Math.addExact(Math.multiplyExact(v.value.epochSecond, 1_000_000_000L), v.value.nano.toLong()) } catch (_: ArithmeticException) { malformed() }
          signed(nanos)
        }
        else -> malformed()
      }
    }
  }
  class Reader(private val data: ByteArray) {
    private var pos = 0
    fun done(): Boolean = pos == data.size
    fun byte(): Int { if (pos >= data.size) malformed(); return data[pos++].toInt() and 255 }
    fun raw(count: Int): ByteArray { if (count < 0 || count > data.size - pos) malformed(); return data.copyOfRange(pos, pos + count).also { pos += count } }
    fun uint(): ULong {
      var n = 0uL
      for (i in 0..9) {
        val b = byte()
        if (i == 9 && b > 1) malformed()
        n = n or ((b and 127).toULong() shl (i * 7))
        if (b and 128 == 0) { if (i > 0 && b == 0) malformed(); return n }
      }
      malformed()
    }
    fun signed(): Long { val n = uint(); return ((n shr 1).toLong() xor -((n and 1uL).toLong())) }
    fun count(): Int { val n = uint(); if (n > MAX_CONTAINER.toULong()) malformed(); return n.toInt() }
    fun blob(): ByteArray = raw(count())
    fun text(): String = try {
      StandardCharsets.UTF_8.newDecoder().onMalformedInput(CodingErrorAction.REPORT).onUnmappableCharacter(CodingErrorAction.REPORT).decode(ByteBuffer.wrap(blob())).toString()
    } catch (_: java.nio.charset.CharacterCodingException) { malformed() }
    fun value(depth: Int = 0): Any? {
      if (depth > 128) malformed()
      return when (byte()) {
        0 -> null
        1 -> false
        2 -> true
        3 -> signed()
        4 -> LatchValue.Unsigned(uint())
        5 -> ByteBuffer.wrap(raw(4)).order(ByteOrder.LITTLE_ENDIAN).float.let { LatchValue.Single(it) }
        6 -> ByteBuffer.wrap(raw(8)).order(ByteOrder.LITTLE_ENDIAN).double
        7 -> text()
        8 -> blob()
        9 -> { val m = linkedMapOf<Long, Any?>(); repeat(count()) { val id = uint(); if (id == 0uL || id > UInt.MAX_VALUE.toULong()) malformed(); val key = id.toLong(); if (m.containsKey(key)) malformed(); m[key] = value(depth + 1) }; LatchValue.Structure(m) }
        10 -> List(count()) { value(depth + 1) }
        11 -> { val m = linkedMapOf<String, Any?>(); repeat(count()) { val key = stringValue(value(depth + 1)); if (m.containsKey(key)) malformed(); m[key] = value(depth + 1) }; m }
        12 -> { val n = signed(); LatchValue.Time(Instant.ofEpochSecond(Math.floorDiv(n, 1_000_000_000L), Math.floorMod(n, 1_000_000_000L))) }
        else -> malformed()
      }
    }
    private fun stringValue(v: Any?): String = v as? String ?: malformed()
  }
  fun encode(value: Any?): ByteArray = Writer().apply { value(value) }.result()
  fun decode(bytes: ByteArray): Any? = Reader(bytes).let { val v = it.value(); if (!it.done()) malformed(); v }
}

internal data class LatchEnvelope(
  val kind: Int, val version: String = "", val id: String = "", val method: String = "",
  val event: String = "", val payload: ByteArray = byteArrayOf(), val error: String = "", val code: String = ""
) {
  fun encode(): ByteArray = LatchBinary.Writer().apply {
    if (!valid()) throw LatchError("malformed_frame", "malformed envelope")
    byte(1); byte(kind); text(version); text(id); text(method); text(event); blob(payload); text(error); text(code)
  }.result()
  private fun valid(): Boolean = when (kind) {
    1, 2 -> version.isNotEmpty()
    3 -> id.isNotEmpty() && method.isNotEmpty()
    4 -> id.isNotEmpty()
    5 -> id.isNotEmpty() && code.isNotEmpty()
    6 -> payload.isNotEmpty()
    7 -> code.isNotEmpty()
    else -> false
  }
  companion object {
    fun decode(bytes: ByteArray): LatchEnvelope {
      val r = LatchBinary.Reader(bytes)
      if (r.byte() != 1) throw LatchError("malformed_frame", "unsupported envelope version")
      val env = LatchEnvelope(r.byte(), r.text(), r.text(), r.text(), r.text(), r.blob(), r.text(), r.text())
      if (!r.done() || env.kind !in 1..7 || !env.valid()) throw LatchError("malformed_frame", "malformed envelope")
      return env
    }
  }
}

enum class ConnectionState { CONNECTING, CONNECTED, OFFLINE }

internal class LatchTransport private constructor(
  private val socket: WebSocket,
  private val onEvent: (ByteArray) -> Unit,
  private val onFailure: ((Throwable) -> Unit)?,
  private val onStateChange: ((ConnectionState) -> Unit)?
) : AutoCloseable {
  private val closed = AtomicBoolean(false)
  private val nextId = AtomicLong(1)
  private val lock = Any()
  private val pending = linkedMapOf<String, CompletableFuture<ByteArray>>()
  private var sending: CompletableFuture<*> = CompletableFuture.completedFuture(Unit)

  fun call(method: String, payload: ByteArray): CompletableFuture<ByteArray> {
    val result = CompletableFuture<ByteArray>()
    synchronized(lock) {
      if (closed.get()) return CompletableFuture.failedFuture(LatchError("connection_closed", "connection closed"))
      val id = nextId.getAndIncrement().toString()
      pending[id] = result
      val frame = LatchEnvelope(3, id = id, method = method, payload = payload).encode()
      // java.net.http forbids overlapping binary sends; chain each send to its predecessor.
      sending = sending.handle { _, _ -> Unit }.thenCompose {
        if (closed.get()) CompletableFuture.failedFuture<WebSocket>(LatchError("connection_closed", "connection closed"))
        else socket.sendBinary(ByteBuffer.wrap(frame), true)
      }.whenComplete { _, err -> if (err != null) fail(err) }
    }
    return result
  }
  private fun deliverEvent(receiver: (ByteArray) -> Unit, payload: ByteArray) {
    try { receiver(payload) } catch (e: Throwable) { fail(e) }
  }
  fun dispatch(env: LatchEnvelope) {
    when (env.kind) {
      4 -> synchronized(lock) { pending.remove(env.id) }?.complete(env.payload)
      5 -> synchronized(lock) { pending.remove(env.id) }?.completeExceptionally(LatchError(env.code, env.error))
      6 -> synchronized(lock) { if (!closed.get()) deliverEvent(onEvent, env.payload) }
      7 -> fail(LatchError(env.code, env.error))
      else -> fail(LatchError("malformed_frame", "unexpected envelope kind"))
    }
  }
  fun fail(error: Throwable) { terminate(error, true) }
  private fun terminate(error: Throwable, abort: Boolean) {
    val waiters = synchronized(lock) {
      if (!closed.compareAndSet(false, true)) return
      pending.values.toList().also { pending.clear() }
    }
    waiters.forEach { it.completeExceptionally(error) }
    try { onStateChange?.invoke(ConnectionState.OFFLINE) } catch (_: Throwable) { /* The connection is already closed. */ }
    try { onFailure?.invoke(error) } catch (_: Throwable) { /* The connection is already failed. */ }
    if (abort) socket.abort()
    else try { socket.sendClose(WebSocket.NORMAL_CLOSURE, "").whenComplete { _, err -> if (err != null) socket.abort() } } catch (_: Throwable) { socket.abort() }
  }
  override fun close() { terminate(LatchError("connection_closed", "connection closed"), false) }
  companion object {
    fun connect(url: String, version: String, onEvent: (ByteArray) -> Unit,
      onFailure: ((Throwable) -> Unit)?, onStateChange: ((ConnectionState) -> Unit)?): CompletableFuture<LatchTransport> {
      val result = CompletableFuture<LatchTransport>()
      try { onStateChange?.invoke(ConnectionState.CONNECTING) }
      catch (e: Throwable) { return CompletableFuture.failedFuture(e) }
      val uri = try {
        val u = URI(url)
        val query = listOfNotNull(u.rawQuery?.split("&")?.filter { it.substringBefore("=") != "version" }?.joinToString("&")?.takeIf { it.isNotEmpty() }, "version=" + java.net.URLEncoder.encode(version, "UTF-8")).joinToString("&")
        URI.create(u.scheme + "://" + u.rawAuthority + (u.rawPath ?: "") + "?" + query + (u.rawFragment?.let { "#$it" } ?: ""))
      } catch (e: Exception) {
        try { onStateChange?.invoke(ConnectionState.OFFLINE) } catch (_: Throwable) { }
        return CompletableFuture.failedFuture(e)
      }
      fun failBeforeOpen(error: Throwable) {
        if (result.completeExceptionally(error)) try { onStateChange?.invoke(ConnectionState.OFFLINE) } catch (_: Throwable) { }
      }
      val listener = object : WebSocket.Listener {
        private var transport: LatchTransport? = null
        fun transport(): LatchTransport = transport ?: throw LatchError("connection_closed", "WebSocket did not open")
        private val fragments = ByteArrayOutputStream()
        override fun onOpen(ws: WebSocket) {
          val connection = LatchTransport(ws, onEvent, onFailure, onStateChange)
          transport = connection
          if (result.isDone) { connection.fail(LatchError("connection_closed", "connection closed")); return }
          try { onStateChange?.invoke(ConnectionState.CONNECTED); ws.request(1) }
          catch (e: Throwable) { result.completeExceptionally(e); connection.fail(e) }
        }
        override fun onBinary(ws: WebSocket, data: ByteBuffer, last: Boolean): CompletableFuture<*>? {
          try {
            val bytes = ByteArray(data.remaining()); data.get(bytes); fragments.write(bytes)
            if (last) {
              val env = LatchEnvelope.decode(fragments.toByteArray()); fragments.reset()
              transport?.dispatch(env)
            }
          } catch (e: Throwable) { if (transport == null) failBeforeOpen(e) else { result.completeExceptionally(e); transport?.fail(e) } }
          ws.request(1)
          return null
        }
        override fun onText(ws: WebSocket, data: CharSequence, last: Boolean): CompletableFuture<*>? {
          val error = LatchError("malformed_frame", "text frame received")
          if (transport == null) failBeforeOpen(error) else { result.completeExceptionally(error); transport?.fail(error) }
          return null
        }
        override fun onError(ws: WebSocket, error: Throwable) {
          if (transport == null) failBeforeOpen(error) else { result.completeExceptionally(error); transport?.fail(error) }
        }
        override fun onClose(ws: WebSocket, statusCode: Int, reason: String): CompletableFuture<*>? {
          val error = LatchError("connection_closed", "connection closed: $statusCode $reason")
          if (transport == null) failBeforeOpen(error) else { result.completeExceptionally(error); transport?.fail(error) }
          return null
        }
      }
      try {
        HttpClient.newHttpClient().newWebSocketBuilder().buildAsync(uri, listener).whenComplete { _, err ->
          if (err != null) {
            val transport = try { listener.transport() } catch (_: Throwable) { null }
            if (transport == null) failBeforeOpen(err)
            else { result.completeExceptionally(err); transport.fail(err) }
          } else if (!result.isDone) {
            val transport = listener.transport()
            if (!transport.closed.get()) result.complete(transport)
            else result.completeExceptionally(LatchError("connection_closed", "connection closed"))
          }
        }
      } catch (e: Throwable) { failBeforeOpen(e) }
      return result
    }
  }
}
`
