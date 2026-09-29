import Foundation
import XCTest
@testable import LatchClient

final class LatchClientTests: XCTestCase {
    func testBinaryCodecRoundTripsSupportedValues() throws {
        let instant = Date(timeIntervalSince1970: 1_700_000_000.125)
        let values: [LatchValue] = [
            .null,
            .bool(false), .bool(true),
            .int(Int64.min), .int(-1), .int(0), .int(Int64.max),
            .uint(0), .uint(UInt64.max),
            .float32(-12.5), .float64(0.125),
            .string("hello 🌍"),
            .time(instant),
            .list([.string("x"), .int(9), .null]),
            .map(["alpha": .bool(true), "beta": .float64(3.5)]),
            .structure([1: .string("nested"), 4: .list([.uint(7)])])
        ]

        for value in values {
            let encoded = try LatchBinary.encodeValue(value)
            let decoded = try LatchBinary.decodeValue(encoded)
            XCTAssertEqual(decoded, value, "round trip for \(value)")
        }
    }

    func testBinaryCodecRejectsMalformedValues() {
        let malformedValues = [
            Data(),
            Data([0xff]),
            Data([7, 2, 0x61]),
            Data([7, 0x80, 0]),
            Data([7, 1, 0xff]),
            Data([10, 0x81, 0x80, 0x80, 0x08]),
            Data([0, 0])
        ]

        for malformed in malformedValues {
            XCTAssertThrowsError(try LatchBinary.decodeValue(malformed))
        }
    }

    func testFailedConnectReportsOffline() async {
        var continuation: AsyncStream<ConnectionState>.Continuation!
        let stream = AsyncStream<ConnectionState> { continuation = $0 }
        let sink = continuation!
        let client = LatchClient(
            url: URL(string: "ws://127.0.0.1:1")!,
            onEvent: { _ in },
            onConnectionStateChange: { sink.yield($0) }
        )
        do {
            _ = try await client.connect()
            XCTFail("expected connection failure")
        } catch {
            var states = stream.makeAsyncIterator()
            let connecting = await states.next()
            let offline = await states.next()
            XCTAssertEqual(connecting, .connecting)
            XCTAssertEqual(offline, .offline)
        }
    }

    func testRPCEventsAndErrorsAgainstGoServer() async throws {
        guard let rawURL = ProcessInfo.processInfo.environment["SERVER_URL"],
              let url = URL(string: rawURL) else {
            throw XCTSkip("SERVER_URL is only configured by the cross-language integration runner")
        }

        var eventContinuation: AsyncStream<Event>.Continuation!
        let eventStream = AsyncStream<Event> { eventContinuation = $0 }
        var stateContinuation: AsyncStream<ConnectionState>.Continuation!
        let stateStream = AsyncStream<ConnectionState> { stateContinuation = $0 }
        let eventSink = eventContinuation!
        let stateSink = stateContinuation!
        let connection = try await LatchClient(
            url: url,
            onEvent: { eventSink.yield($0) },
            onConnectionStateChange: { stateSink.yield($0) }
        ).connect()
        defer { Task { await connection.close() } }
        var states = stateStream.makeAsyncIterator()
        let connecting = await states.next()
        XCTAssertEqual(connecting, .connecting)
        let connected = await states.next()
        XCTAssertEqual(connected, .connected)
        let presenceTask = Task { try await nextEvent(eventStream, matching: "presence") }
        let joined = try await connection.chatJoinRoom(
            JoinRoomRequest(room: "general", userId: "swift-user")
        )
        XCTAssertEqual(joined.room, "general")
        XCTAssertTrue(joined.memberIds.contains("swift-user"))
        let presence = try await presenceTask.value
        XCTAssertEqual(presence.presence?.userId, "swift-user")

        let messageTask = Task { try await nextEvent(eventStream, matching: "message") }
        let sent = try await connection.chatSendMessage(
            SendMessageRequest(room: "general", senderId: "swift-user", text: "swift says hello")
        )
        XCTAssertEqual(sent.message.text, "swift says hello")
        XCTAssertGreaterThan(sent.message.id, 0)
        XCTAssertLessThan(abs(sent.message.sentAt.timeIntervalSinceNow), 30)

        let messageEvent = try await messageTask.value
        XCTAssertEqual(messageEvent.message?.message.text, "swift says hello")

        let history = try await connection.chatHistory(HistoryRequest(room: "general"))
        XCTAssertTrue(history.messages.contains(where: { $0.text == "swift says hello" }))
        let rooms = try await connection.chatListRooms(ListRoomsRequest())
        XCTAssertEqual(rooms.rooms, ["general", "random"])

        do {
            _ = try await connection.chatSendMessage(
                SendMessageRequest(room: "", senderId: "swift-user", text: "")
            )
            XCTFail("expected server RPC error")
        } catch let error as LatchError {
            XCTAssertEqual(error.code, "invalid_request")
            XCTAssertEqual(error.message, "room, senderId, and text are required")
        }

        await connection.close()
        let offline = await states.next()
        XCTAssertEqual(offline, .offline)
        do {
            _ = try await connection.chatListRooms(ListRoomsRequest())
            XCTFail("RPC after close should fail")
        } catch let error as LatchError {
            XCTAssertEqual(error.code, "connection_closed")
        }
    }

    private func nextEvent(_ events: AsyncStream<Event>, matching kind: String) async throws -> Event {
        for await event in events where event.kind == kind {
            return event
        }
        throw LatchError(code: "event_stream_closed", message: "event stream closed before \(kind)")
    }
}
