package kotlin

import (
	"context"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"

	"strings"
	"testing"
	"time"

	"github.com/vehmloewff/latch/wire"
)

// Compiles the actual generated source, rather than testing a hand-written
// approximation of its codec. Skips on machines without a Kotlin/JVM toolchain.
func TestGeneratedKotlinExecutable(t *testing.T) {
	compiler, err := exec.LookPath("kotlinc")
	if err != nil {
		t.Skip("kotlinc not installed")
	}
	java, err := exec.LookPath("java")
	if err != nil {
		t.Skip("Java not installed")
	}
	// macOS's /usr/bin/java can be a shim without an installed JDK.
	if out, err := exec.Command(java, "-version").CombinedOutput(); err != nil {
		t.Skipf("Java runtime unavailable: %s", out)
	}
	p := fixture()
	// A sparse field ID must coexist with the positional IDs in this fixture.
	p.Types[1].Fields[0].Number = 13
	src := source(t, p, Options{})

	vector, err := wire.Encode(struct {
		A int64  `latch:"7"`
		B []byte `latch:"19"`
	}{A: -9223372036854775808, B: []byte{0, 255}})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	generated := filepath.Join(dir, "LatchClient.kt")
	testSource := filepath.Join(dir, "RuntimeTest.kt")
	if err := os.WriteFile(generated, []byte(src), 0600); err != nil {
		t.Fatal(err)
	}
	harness := strings.ReplaceAll(kotlinHarness, "GO_VECTOR", hex.EncodeToString(vector))
	if err := os.WriteFile(testSource, []byte(harness), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	jar := filepath.Join(dir, "test.jar")
	cmd := exec.CommandContext(ctx, compiler, generated, testSource, "-include-runtime", "-d", jar)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("kotlinc: %v\n%s", err, out)
	}
	cmd = exec.CommandContext(ctx, java, "-jar", jar)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("Kotlin runtime: %v\n%s", err, out)
	}
}

const kotlinHarness = `import java.lang.reflect.Proxy
import java.net.http.WebSocket
import java.nio.ByteBuffer
import java.util.concurrent.CompletableFuture
import java.util.concurrent.CompletionException
import java.util.concurrent.TimeUnit

fun hex(bytes: ByteArray): String = bytes.joinToString("") { "%02x".format(it.toInt() and 255) }
fun unhex(s: String): ByteArray = s.chunked(2).map { it.toInt(16).toByte() }.toByteArray()
fun checkBad(data: String) { try { LatchBinary.decode(unhex(data)); error("accepted invalid value: $data") } catch (_: LatchError) {} }
fun rejected(f: CompletableFuture<*>) { try { f.get(3, TimeUnit.SECONDS); error("future succeeded") } catch (_: java.util.concurrent.ExecutionException) {} }
fun main() {
  val vector = unhex("GO_VECTOR")
  val obj = LatchBinary.decode(vector) as LatchValue.Structure
  check(obj.fields[7L] == Long.MIN_VALUE)
  check((obj.fields[19L] as ByteArray).contentEquals(byteArrayOf(0, -1)))
  check(hex(LatchBinary.encode(obj)) == hex(vector))
  val highField = LatchValue.Structure(mapOf(4294967295L to 1L))
  check((LatchBinary.decode(LatchBinary.encode(highField)) as LatchValue.Structure).fields[4294967295L] == 1L)
  try { LatchBinary.encode(LatchValue.Structure(mapOf(4294967296L to 1L))); error("accepted out-of-range field ID") } catch (_: LatchError) {}
  check(hex(LatchBinary.encode(LatchValue.Unsigned(ULong.MAX_VALUE))) == "04ffffffffffffffffff01")
  check(LatchValue.unsigned(LatchBinary.decode(unhex("04ffffffffffffffffff01"))) == ULong.MAX_VALUE)
  check(hex(LatchBinary.encode(LatchValue.Time(java.time.Instant.ofEpochSecond(-1, 999999999)))) == "0c01")
  check(hex(LatchBinary.encode(LatchValue.Single(1.5f))) == "050000c03f")
  check(hex(LatchBinary.encode(1.5)) == "06000000000000f83f")
  check(hex(LatchBinary.encode(LatchBinary.decode(unhex("053412c07f")))) == "053412c07f")
  check(hex(LatchBinary.encode(LatchBinary.decode(unhex("06341200000000f87f")))) == "06341200000000f87f")
  check(hex(LatchBinary.encode(listOf(null, true, "é"))) == "0a0300020702c3a9")
  checkBad("038000") // overlong varint
  checkBad("07ff") // length overflow
  checkBad("0701ff") // invalid UTF-8
  checkBad("09010000") // zero field ID
  checkBad("090107000700") // duplicate field ID
  checkBad("0b020701610007016100") // duplicate map key
  checkBad("030001") // trailing data
  checkBad("05") // truncated float
  checkBad("04ffffffffffffffffffff02") // overflow varint
  val packet = Packet("hi", State.Open, null, blob = byteArrayOf(1), history = emptyList(), tags = emptyMap())
  val decoded = Packet.fromLatch(LatchBinary.decode(LatchBinary.encode(packet.toLatch())))
  check(decoded.text == "hi" && decoded.nullable == null && decoded.optional == null)
  check((packet.toLatch() as LatchValue.Structure).fields[13L] == "hi")
  check(!(packet.toLatch() as LatchValue.Structure).fields.containsKey(1L))
  check((packet.toLatch() as LatchValue.Structure).fields[3L] == null)
  check(!(packet.toLatch() as LatchValue.Structure).fields.containsKey(4L))
  val withPointer = Packet("hi", State.Closed, "set", "some", byteArrayOf(1), emptyList(), emptyMap())
  check(Packet.fromLatch(LatchBinary.decode(LatchBinary.encode(withPointer.toLatch()))).nullable == "set")
  check((withPointer.toLatch() as LatchValue.Structure).fields[3L] == "set")
  var sendError: Throwable? = null
  var throwOnSend = false
  var failAsync = false
  var delayedSend: CompletableFuture<WebSocket>? = null
  var aborted = false
  val sent = mutableListOf<ByteArray>()
  lateinit var socket: WebSocket
  socket = Proxy.newProxyInstance(WebSocket::class.java.classLoader, arrayOf(WebSocket::class.java)) { _, method, args ->
    when (method.name) {
      "sendBinary" -> {
        if (throwOnSend) throw IllegalStateException("sync send failed")
        val buf = args!![0] as ByteBuffer
        val bytes = ByteArray(buf.remaining()); buf.get(bytes); sent.add(bytes)
        delayedSend?.also { delayedSend = null } ?: if (failAsync) CompletableFuture.failedFuture<WebSocket>(IllegalStateException("async send failed")) else CompletableFuture.completedFuture(socket)
      }
      "sendClose" -> CompletableFuture.completedFuture(socket)
      "abort" -> { aborted = true; null }
      else -> error("unexpected WebSocket method: ${method.name}")
    }
  } as WebSocket
  val events = mutableListOf<String>()
  fun transport(receiver: (ByteArray) -> Unit = { events.add(LatchValue.string(LatchBinary.decode(it))) }): LatchTransport =
    LatchTransport::class.java.getDeclaredConstructor(WebSocket::class.java, kotlin.jvm.functions.Function1::class.java,
      kotlin.jvm.functions.Function1::class.java, kotlin.jvm.functions.Function1::class.java).apply { isAccessible = true }
      .newInstance(socket, receiver, { e: Throwable -> sendError = e }, null)
  val conn = transport()
  conn.dispatch(LatchEnvelope(6, payload = LatchBinary.encode("early")))
  check(events == listOf("early"))
  val response = conn.call("ping", byteArrayOf(0))
  val frame = LatchEnvelope.decode(sent.single())
  check(frame.kind == 3 && frame.method == "ping" && frame.id == "1")
  conn.dispatch(LatchEnvelope(4, id = frame.id, payload = byteArrayOf(0)))
  check(response.get(3, TimeUnit.SECONDS).contentEquals(byteArrayOf(0)))
  val bad = transport { throw IllegalStateException("bad callback") }
  val waiting = bad.call("ping", byteArrayOf(0))
  bad.dispatch(LatchEnvelope(6, payload = byteArrayOf(0)))
  rejected(waiting)
  check(sendError?.message == "bad callback" && aborted)
  rejected(bad.call("ping", byteArrayOf(0)))
  failAsync = true; aborted = false
  val async = transport().call("ping", byteArrayOf(0))
  rejected(async); check(aborted)
  failAsync = false; throwOnSend = true; aborted = false
  rejected(transport().call("ping", byteArrayOf(0))); check(aborted)
  throwOnSend = false; aborted = false
  val closing = transport()
  val hold = CompletableFuture<WebSocket>()
  delayedSend = hold
  val pending = closing.call("ping", byteArrayOf(0))
  val next = closing.call("ping", byteArrayOf(0))
  val count = sent.size
  check(next.isDone.not() && sent.size == count) // the second send waits for the first
  closing.close()
  rejected(pending); rejected(next); check(!aborted)
  hold.complete(socket)
  check(sent.size == count) // queued sends must not run after close
  rejected(closing.call("ping", byteArrayOf(0)))
  val envelope = LatchEnvelope(5, id = "7", error = "bad", code = "denied")
  check(LatchEnvelope.decode(envelope.encode()).code == "denied")
  try { LatchEnvelope.decode(LatchEnvelope(4).encode()); error("accepted response without id") } catch (_: LatchError) {}
  println("Kotlin binary and transport checks passed")
}
`
