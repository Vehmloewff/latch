// Executed against the Go chat server by go run ./integration_test kotlin.
import java.util.concurrent.CountDownLatch
import java.util.concurrent.ExecutionException
import java.util.concurrent.TimeUnit

private fun <T> await(future: java.util.concurrent.CompletableFuture<T>): T = future.get(5, TimeUnit.SECONDS)

fun main() {
  val url = System.getenv("SERVER_URL") ?: error("SERVER_URL is required")
  val alice = await(LatchClient(url).connect())
  val bob = await(LatchClient(url).connect())
  try {
    val presence = CountDownLatch(1)
    val message = CountDownLatch(1)
    alice.onEvent = { event ->
      if (event.kind == "presence" && event.presence?.userId == "bob") presence.countDown()
      if (event.kind == "message" && event.message?.message?.text == "Hello, Kotlin!") message.countDown()
    }
    check(await(alice.chatListRooms(ListRoomsRequest())).rooms.containsAll(listOf("general", "random")))
    check(await(alice.chatJoinRoom(JoinRoomRequest("general", "alice"))).memberIds.contains("alice"))
    check(await(bob.chatJoinRoom(JoinRoomRequest("general", "bob"))).memberIds.contains("alice"))
    check(presence.await(5, TimeUnit.SECONDS)) { "missing presence event" }
    val sent = await(bob.chatSendMessage(SendMessageRequest("general", "bob", "Hello, Kotlin!"))).message
    check(sent.id > 0 && sent.senderId == "bob" && sent.sentAt.epochSecond > 0)
    check(message.await(5, TimeUnit.SECONDS)) { "missing message event" }
    check(await(alice.chatHistory(HistoryRequest("general"))).messages.any { it.id == sent.id })
    try {
      await(alice.chatSendMessage(SendMessageRequest("", "alice", "invalid")))
      error("invalid request succeeded")
    } catch (e: ExecutionException) {
      check((e.cause as LatchError).code == "invalid_request")
    }
  } finally {
    alice.close()
    bob.close()
  }
  try {
    await(alice.chatListRooms(ListRoomsRequest()))
    error("request after close succeeded")
  } catch (e: ExecutionException) {
    check((e.cause as LatchError).code == "connection_closed")
  }
  println("Kotlin chat integration passed")
}
