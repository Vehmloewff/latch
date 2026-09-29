import CryptoKit
import Darwin
import Foundation
import XCTest
@testable import LatchClient

final class ReconnectTests: XCTestCase {
    func testSentRPCFailsAndOfflineRPCWaitsForReconnect() async throws {
        let server = try DroppingWebSocketServer()
        defer { server.stop() }
        var stateSink: AsyncStream<ConnectionState>.Continuation!
        let stateStream = AsyncStream<ConnectionState> { stateSink = $0 }
        var eventSink: AsyncStream<Event>.Continuation!
        let eventStream = AsyncStream<Event> { eventSink = $0 }
        let stateContinuation = stateSink!
        let eventContinuation = eventSink!
        let connection = try await LatchClient(
            url: server.url.appending(queryItems: [URLQueryItem(name: "version", value: "stale")]),
            onEvent: { eventContinuation.yield($0) },
            onConnectionStateChange: { stateContinuation.yield($0) },
            onRequestConstructed: { Self.customizeRequest(&$0) }
        ).connect()
        defer { Task { await connection.close() } }
        let watchdog = makeWatchdog(connection, stateContinuation, eventContinuation)
        defer { watchdog.cancel() }

        let first = Task {
            try await connection.chatSendMessage(
                SendMessageRequest(room: "general", senderId: "test", text: "once")
            )
        }
        do {
            _ = try await first.value
            XCTFail("sent RPC must fail when its socket disappears")
        } catch {
            XCTAssertFalse(error is CancellationError, "sent RPC should fail with a transport error")
        }
        var states = stateStream.makeAsyncIterator()
        let connecting = await states.next()
        let connected = await states.next()
        let offline = await states.next()
        XCTAssertEqual([connecting, connected, offline], [.connecting, .connected, .offline])
        XCTAssertEqual(server.firstMethod, "chat_send_message")

        // The server deliberately refuses new handshakes until this call is queued.
        let queued = Task { try await connection.chatListRooms(ListRoomsRequest()) }
        try await Task.sleep(for: .milliseconds(150))
        server.allowReconnect()
        let rooms = try await queued.value
        XCTAssertEqual(rooms.rooms, ["restored"])
        let retrying = await states.next()
        let restored = await states.next()
        XCTAssertEqual([retrying, restored], [.connecting, .connected])
        var events = eventStream.makeAsyncIterator()
        let event = await events.next()
        XCTAssertEqual(event?.kind, "restored")
        XCTAssertTrue(server.waitForObservations(), "replacement socket did not finish observing requests")
        assertRequests(server)
        await connection.close()
        let closed = await states.next()
        XCTAssertEqual(closed, .offline)
    }

    private static func customizeRequest(_ request: inout URLRequest) {
        request.setValue("swift-test-token", forHTTPHeaderField: "X-Latch-Test")
        var components = URLComponents(url: request.url!, resolvingAgainstBaseURL: false)!
        components.queryItems = (components.queryItems ?? []) + [
            URLQueryItem(name: "source", value: "swift"),
            URLQueryItem(name: "version", value: "wrong")
        ]
        request.url = components.url!
    }

    private func assertRequests(_ server: DroppingWebSocketServer) {
        XCTAssertEqual(server.receivedMethods, ["chat_send_message", "chat_list_rooms"])
        let handshakes = server.handshakes
        XCTAssertEqual(handshakes.count, 2)
        for handshake in handshakes {
            XCTAssertTrue(handshake.contains("X-Latch-Test: swift-test-token"), handshake)
            XCTAssertTrue(handshake.contains("GET /ws?source=swift&version=1 HTTP/1.1"), handshake)
        }
    }

    private func makeWatchdog(
        _ connection: ConnectedLatchClient,
        _ states: AsyncStream<ConnectionState>.Continuation,
        _ events: AsyncStream<Event>.Continuation
    ) -> Task<Void, Never> {
        Task {
            do { try await Task.sleep(for: .seconds(9)) } catch { return }
            states.finish()
            events.finish()
            await connection.close()
        }
    }
}

// A small loopback WebSocket peer: it drops the first RPC without a reply, rejects
// connections while offline, then responds to exactly one RPC on the replacement socket.
private final class DroppingWebSocketServer {
    let url: URL
    private let listener: Int32
    private let lock = NSLock()
    private var methods: [String] = []
    private var requests: [String] = []
    private let reconnect = DispatchSemaphore(value: 0)
    private let observed = DispatchSemaphore(value: 0)
    private let finished = DispatchSemaphore(value: 0)
    private var stopped = false

    var firstMethod: String? { receivedMethods.first }
    var receivedMethods: [String] { lock.lock(); defer { lock.unlock() }; return methods }
    var handshakes: [String] { lock.lock(); defer { lock.unlock() }; return requests }

    init() throws {
        let listeningSocket = socket(AF_INET, SOCK_STREAM, 0)
        guard listeningSocket >= 0 else { throw ServerError.socket }
        var address = sockaddr_in()
        address.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        address.sin_family = sa_family_t(AF_INET)
        address.sin_addr = in_addr(s_addr: in_addr_t(0x0100007f))
        let bound = withUnsafePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) {
                bind(listeningSocket, $0, socklen_t(MemoryLayout<sockaddr_in>.size))
            }
        }
        guard bound == 0, listen(listeningSocket, 8) == 0 else {
            Darwin.close(listeningSocket)
            throw ServerError.socket
        }
        var size = socklen_t(MemoryLayout<sockaddr_in>.size)
        let named = withUnsafeMutablePointer(to: &address) {
            $0.withMemoryRebound(to: sockaddr.self, capacity: 1) { getsockname(listeningSocket, $0, &size) }
        }
        guard named == 0 else { Darwin.close(listeningSocket); throw ServerError.socket }
        url = URL(string: "ws://127.0.0.1:\(UInt16(bigEndian: address.sin_port))/ws")!
        listener = listeningSocket
        DispatchQueue.global().async { [self] in
            defer { finished.signal() }
            serve()
        }
    }

    func allowReconnect() { reconnect.signal() }
    func waitForObservations() -> Bool { observed.wait(timeout: .now() + 4) == .success }
    func stop() {
        lock.lock(); stopped = true; lock.unlock()
        reconnect.signal()
        Darwin.close(listener)
        _ = finished.wait(timeout: .now() + 5)
    }

    private func serve() {
        guard let first = acceptPeer() else { return }
        defer { Darwin.close(first) }
        guard handshake(first), let method = readRequest(first) else { return }
        record(method)
        // Closing the socket (without a response) must fail the already-sent action.
        shutdown(first, SHUT_RDWR)
        reconnect.wait()
        lock.lock(); let shouldStop = stopped; lock.unlock()
        if shouldStop { return }
        guard let second = acceptPeer() else { return }
        defer { Darwin.close(second) }
        guard handshake(second), let method = readRequest(second) else { return }
        record(method)
        let payload = try? LatchBinary.encode(ListRoomsResponse(rooms: ["restored"]))
        if let payload {
            sendFrame(second, envelope(kind: 4, id: "1", payload: payload))
            let event = try? LatchBinary.encode(Event(kind: "restored"))
            if let event { sendFrame(second, envelope(kind: 6, payload: event)) }
        }
        // Catch any accidental replay after the queued RPC has been answered.
        if let duplicate = readRequest(second) { record(duplicate) }
        observed.signal()
    }

    private func record(_ method: String) {
        lock.lock(); methods.append(method); lock.unlock()
    }

    private func acceptPeer() -> Int32? {
        let socketFD = accept(listener, nil, nil)
        guard socketFD >= 0 else { return nil }
        var timeout = timeval(tv_sec: 1, tv_usec: 0)
        _ = setsockopt(socketFD, SOL_SOCKET, SO_RCVTIMEO, &timeout, socklen_t(MemoryLayout<timeval>.size))
        return socketFD
    }

    private func handshake(_ socketFD: Int32) -> Bool {
        var header = Data()
        while !header.suffix(4).elementsEqual([13, 10, 13, 10]), header.count < 8192 {
            guard let byte = readBytes(socketFD, count: 1) else { return false }
            header.append(byte)
        }
        guard let text = String(data: header, encoding: .utf8),
              let keyLine = text.split(separator: "\r\n").first(where: { $0.lowercased().hasPrefix("sec-websocket-key:") }),
              let key = keyLine.split(separator: ":", maxSplits: 1).last?.trimmingCharacters(in: .whitespaces) else {
            return false
        }
        lock.lock(); requests.append(text); lock.unlock()
        let digest = Insecure.SHA1.hash(data: Data((key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").utf8))
        let response = "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\n" +
            "Connection: Upgrade\r\nSec-WebSocket-Accept: \(Data(digest).base64EncodedString())\r\n\r\n"
        return writeBytes(socketFD, Data(response.utf8))
    }

    private func readRequest(_ socketFD: Int32) -> String? {
        while let header = readBytes(socketFD, count: 2) {
            let opcode = header[0] & 15
            var count = Int(header[1] & 127)
            if count == 126 {
                guard let length = readBytes(socketFD, count: 2) else { return nil }
                count = Int(length[0]) << 8 | Int(length[1])
            } else if count == 127 { return nil }
            guard count < 65536 else { return nil }
            let masked = header[1] & 128 != 0
            guard let mask = readBytes(socketFD, count: masked ? 4 : 0),
                  var payload = readBytes(socketFD, count: count) else { return nil }
            if masked { for index in payload.indices { payload[index] ^= mask[index % 4] } }
            if opcode == 9 { sendControl(socketFD, opcode: 10, payload: payload); continue }
            guard opcode == 2 else { return nil }
            return requestMethod(payload)
        }
        return nil
    }

    private func requestMethod(_ payload: Data) -> String? {
        var offset = 2
        guard payload.count > 2, payload[0] == 1, payload[1] == 3 else { return nil }
        // Envelope strings: version, request ID, method.
        guard readBlob(payload, offset: &offset) != nil,
              readBlob(payload, offset: &offset) != nil,
              let method = readBlob(payload, offset: &offset) else { return nil }
        return String(data: method, encoding: .utf8)
    }

    private func sendControl(_ socketFD: Int32, opcode: UInt8, payload: Data) {
        _ = writeBytes(socketFD, Data([0x80 | opcode, UInt8(payload.count)]) + payload)
    }

    private func sendFrame(_ socketFD: Int32, _ data: Data) {
        var frame = Data([0x82])
        if data.count < 126 {
            frame.append(UInt8(data.count))
        } else {
            frame.append(contentsOf: [126, UInt8(data.count >> 8), UInt8(data.count & 255)])
        }
        frame.append(data)
        _ = writeBytes(socketFD, frame)
    }

    private func envelope(kind: UInt8, id: String = "", payload: Data) -> Data {
        var data = Data([1, kind])
        for part in ["", id, "", ""] { appendBlob(Data(part.utf8), to: &data) }
        appendBlob(payload, to: &data)
        appendBlob(Data(), to: &data)
        appendBlob(Data(), to: &data)
        return data
    }

    private func appendBlob(_ bytes: Data, to data: inout Data) {
        var size = bytes.count
        repeat {
            let more = size >= 128
            data.append(UInt8(size & 127) | (more ? 128 : 0))
            size >>= 7
        } while size > 0
        data.append(bytes)
    }

    private func readBlob(_ data: Data, offset: inout Int) -> Data? {
        var size = 0
        var shift = 0
        while offset < data.count, shift < 28 {
            let byte = data[offset]; offset += 1
            size |= Int(byte & 127) << shift
            if byte & 128 == 0 {
                guard size <= data.count - offset else { return nil }
                defer { offset += size }
                return data.subdata(in: offset..<offset + size)
            }
            shift += 7
        }
        return nil
    }

    private func readBytes(_ socketFD: Int32, count: Int) -> Data? {
        var data = Data(count: count)
        var offset = 0
        while offset < count {
            let received = data.withUnsafeMutableBytes {
                Darwin.recv(socketFD, $0.baseAddress!.advanced(by: offset), count - offset, 0)
            }
            guard received > 0 else { return nil }
            offset += received
        }
        return data
    }

    private func writeBytes(_ socketFD: Int32, _ data: Data) -> Bool {
        data.withUnsafeBytes { buffer in
            var offset = 0
            while offset < buffer.count {
                let sent = Darwin.send(socketFD, buffer.baseAddress!.advanced(by: offset), buffer.count - offset, 0)
                guard sent > 0 else { return false }
                offset += sent
            }
            return true
        }
    }

    private enum ServerError: Error { case socket }
}
