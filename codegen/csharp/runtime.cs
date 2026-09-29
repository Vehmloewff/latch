// Embedded into the generated standalone C# file after its using directives.
public sealed class LatchError : Exception
{
    public string Code { get; }
    public LatchError(string code, string message) : base(message) => Code = code;
}

internal static class LatchBinary
{
    private const int MaxContainer = 1 << 24;
    private const int MaxDepth = 128;
    private static readonly System.Text.UTF8Encoding Utf8 = new System.Text.UTF8Encoding(false, true);
    private static FormatException Malformed() => new FormatException("Malformed Latch binary value");

    internal sealed class Writer
    {
        private readonly System.IO.MemoryStream _stream = new System.IO.MemoryStream();
        internal void Byte(byte value) => _stream.WriteByte(value);
        internal void Raw(byte[] value) => _stream.Write(value, 0, value.Length);
        internal void UInt(ulong value)
        {
            do
            {
                byte part = (byte)(value & 127);
                value >>= 7;
                Byte((byte)(part | (value == 0 ? 0 : 128)));
            } while (value != 0);
        }
        internal void Signed(long value) => UInt(unchecked(((ulong)value << 1) ^ (ulong)(value >> 63)));
        internal void Blob(byte[] value) { UInt((ulong)value.Length); Raw(value); }
        internal void Text(string value)
        {
            try { Blob(Utf8.GetBytes(value)); }
            catch (System.Text.EncoderFallbackException) { throw Malformed(); }
        }
        internal byte[] Result() => _stream.ToArray();

        internal void Value(object? value, int depth = 0)
        {
            if (depth > MaxDepth) throw Malformed();
            switch (value)
            {
                case null: Byte(0); break;
                case bool b: Byte((byte)(b ? 2 : 1)); break;
                case long signed64: Byte(3); Signed(signed64); break;
                case int signed32: Byte(3); Signed(signed32); break;
                case short signed16: Byte(3); Signed(signed16); break;
                case sbyte signed8: Byte(3); Signed(signed8); break;
                case ulong unsigned64: Byte(4); UInt(unsigned64); break;
                case uint unsigned32: Byte(4); UInt(unsigned32); break;
                case ushort unsigned16: Byte(4); UInt(unsigned16); break;
                case byte unsigned8: Byte(4); UInt(unsigned8); break;
                case float single:
                    Byte(5);
                    var f = new byte[4];
                    System.Buffers.Binary.BinaryPrimitives.WriteInt32LittleEndian(f, BitConverter.SingleToInt32Bits(single));
                    Raw(f);
                    break;
                case double real:
                    Byte(6);
                    var d = new byte[8];
                    System.Buffers.Binary.BinaryPrimitives.WriteInt64LittleEndian(d, BitConverter.DoubleToInt64Bits(real));
                    Raw(d);
                    break;
                case string s: Byte(7); Text(s); break;
                case byte[] bytes: Byte(8); Blob(bytes); break;
                case Dictionary<ulong, object?> fields:
                    if (fields.Count > MaxContainer) throw Malformed();
                    Byte(9); UInt((ulong)fields.Count);
                    foreach (var field in fields.OrderBy(entry => entry.Key))
                    {
                        if (field.Key == 0 || field.Key > uint.MaxValue) throw Malformed();
                        UInt(field.Key); Value(field.Value, depth + 1);
                    }
                    break;
                case List<object?> items:
                    if (items.Count > MaxContainer) throw Malformed();
                    Byte(10); UInt((ulong)items.Count);
                    foreach (var item in items) Value(item, depth + 1);
                    break;
                case Dictionary<string, object?> entries:
                    if (entries.Count > MaxContainer) throw Malformed();
                    Byte(11); UInt((ulong)entries.Count);
                    foreach (var entry in entries.OrderBy(entry => entry.Key, StringComparer.Ordinal))
                    {
                        Byte(7); Text(entry.Key); Value(entry.Value, depth + 1);
                    }
                    break;
                case DateTimeOffset time:
                    Byte(12);
                    try { Signed(checked((time.ToUniversalTime().Ticks - DateTimeOffset.UnixEpoch.Ticks) * 100)); }
                    catch (OverflowException) { throw Malformed(); }
                    break;
                default: throw new ArgumentException("Unsupported Latch value type: " + value.GetType());
            }
        }
    }

    internal sealed class Reader
    {
        private readonly byte[] _data;
        private int _pos;
        internal Reader(byte[] data) => _data = data;
        internal bool Done => _pos == _data.Length;
        internal byte Byte()
        {
            if (_pos == _data.Length) throw Malformed();
            return _data[_pos++];
        }
        internal byte[] Raw(int count)
        {
            if (count < 0 || count > _data.Length - _pos) throw Malformed();
            var result = new byte[count];
            Buffer.BlockCopy(_data, _pos, result, 0, count);
            _pos += count;
            return result;
        }
        internal ulong UInt()
        {
            ulong value = 0;
            for (int i = 0; i < 10; i++)
            {
                byte b = Byte();
                if (i == 9 && b > 1) throw Malformed();
                value |= (ulong)(b & 127) << (i * 7);
                if ((b & 128) == 0)
                {
                    if (i > 0 && b == 0) throw Malformed();
                    return value;
                }
            }
            throw Malformed();
        }
        internal long Signed()
        {
            ulong n = UInt();
            return unchecked((long)(n >> 1) ^ -(long)(n & 1));
        }
        internal int Count()
        {
            ulong n = UInt();
            if (n > MaxContainer) throw Malformed();
            return (int)n;
        }
        internal byte[] Blob() => Raw(Count());
        internal string Text()
        {
            try { return Utf8.GetString(Blob()); }
            catch (System.Text.DecoderFallbackException) { throw Malformed(); }
        }
        internal object? Value(int depth = 0)
        {
            if (depth > MaxDepth) throw Malformed();
            switch (Byte())
            {
                case 0: return null;
                case 1: return false;
                case 2: return true;
                case 3: return Signed();
                case 4: return UInt();
                case 5: return BitConverter.Int32BitsToSingle(System.Buffers.Binary.BinaryPrimitives.ReadInt32LittleEndian(Raw(4)));
                case 6: return BitConverter.Int64BitsToDouble(System.Buffers.Binary.BinaryPrimitives.ReadInt64LittleEndian(Raw(8)));
                case 7: return Text();
                case 8: return Blob();
                case 9:
                    var fields = new Dictionary<ulong, object?>();
                    int fieldCount = Count();
                    for (int i = 0; i < fieldCount; i++)
                    {
                        ulong id = UInt();
                        if (id == 0 || id > uint.MaxValue || fields.ContainsKey(id)) throw Malformed();
                        fields.Add(id, Value(depth + 1));
                    }
                    return fields;
                case 10:
                    var items = new List<object?>();
                    int itemCount = Count();
                    for (int i = 0; i < itemCount; i++) items.Add(Value(depth + 1));
                    return items;
                case 11:
                    var entries = new Dictionary<string, object?>();
                    int entryCount = Count();
                    for (int i = 0; i < entryCount; i++)
                    {
                        if (Byte() != 7) throw Malformed();
                        string key = Text();
                        if (entries.ContainsKey(key)) throw Malformed();
                        entries.Add(key, Value(depth + 1));
                    }
                    return entries;
                case 12:
                    long nanos = Signed();
                    try { return DateTimeOffset.UnixEpoch.AddTicks(nanos / 100); }
                    catch (ArgumentOutOfRangeException) { throw Malformed(); }
                default: throw Malformed();
            }
        }
    }

    internal static byte[] Encode(object? value)
    {
        var writer = new Writer(); writer.Value(value); return writer.Result();
    }
    internal static object? Decode(byte[] data)
    {
        var reader = new Reader(data);
        object? value = reader.Value();
        if (!reader.Done) throw Malformed();
        return value;
    }
}

internal sealed class LatchEnvelope
{
    internal byte Kind { get; set; }
    internal string Version { get; set; } = "";
    internal string ID { get; set; } = "";
    internal string Method { get; set; } = "";
    internal string Event { get; set; } = "";
    internal byte[] Payload { get; set; } = Array.Empty<byte>();
    internal string Error { get; set; } = "";
    internal string Code { get; set; } = "";

    private void Validate()
    {
        bool valid = Kind switch
        {
            1 or 2 => Version.Length != 0,
            3 => ID.Length != 0 && Method.Length != 0,
            4 => ID.Length != 0,
            5 => ID.Length != 0 && Code.Length != 0,
            6 => Payload.Length != 0,
            7 => Code.Length != 0,
            _ => false
        };
        if (!valid) throw new FormatException("Malformed Latch envelope");
    }
    internal byte[] Encode()
    {
        Validate();
        var writer = new LatchBinary.Writer();
        writer.Byte(1); writer.Byte(Kind);
        writer.Text(Version); writer.Text(ID); writer.Text(Method); writer.Text(Event);
        writer.Blob(Payload); writer.Text(Error); writer.Text(Code);
        return writer.Result();
    }
    internal static LatchEnvelope Decode(byte[] data)
    {
        var reader = new LatchBinary.Reader(data);
        if (reader.Byte() != 1) throw new FormatException("Unsupported Latch envelope version");
        var envelope = new LatchEnvelope
        {
            Kind = reader.Byte(), Version = reader.Text(), ID = reader.Text(),
            Method = reader.Text(), Event = reader.Text(), Payload = reader.Blob(),
            Error = reader.Text(), Code = reader.Text()
        };
        if (!reader.Done) throw new FormatException("Trailing Latch envelope data");
        envelope.Validate();
        return envelope;
    }
}

public enum ConnectionState { Connecting, Connected, Offline }

internal sealed class LatchTransport : IAsyncDisposable
{
    private const int MaxFrame = 1 << 24;
    private readonly Uri _uri;
    private readonly System.Threading.CancellationTokenSource _stop = new();
    private readonly System.Threading.SemaphoreSlim _sendLock = new(1, 1);
    private readonly object _gate = new();
    private readonly Queue<(string ID, byte[] Frame, TaskCompletionSource<byte[]> Waiter)> _queued = new();
    private readonly Dictionary<string, TaskCompletionSource<byte[]>> _pending = new();
    private readonly Action<byte[]> _onEvent;
    private readonly Action<Exception>? _onFailure;
    private readonly Action<ConnectionState>? _onStateChange;
    private readonly Action<System.Net.WebSockets.ClientWebSocketOptions>? _onRequestConstructed;
    private readonly TaskCompletionSource<LatchTransport> _initial = new(TaskCreationOptions.RunContinuationsAsynchronously);
    private System.Net.WebSockets.ClientWebSocket? _socket;
    private System.Net.WebSockets.ClientWebSocket? _attempt;
    private long _nextID;
    private bool _closed;
    private ConnectionState _state = ConnectionState.Connecting;

    private LatchTransport(Uri uri, Action<byte[]> onEvent, Action<Exception>? onFailure, Action<ConnectionState>? onStateChange,
        Action<System.Net.WebSockets.ClientWebSocketOptions>? onRequestConstructed)
    { _uri = uri; _onEvent = onEvent; _onFailure = onFailure; _onStateChange = onStateChange; _onRequestConstructed = onRequestConstructed; }

    internal static async Task<LatchTransport> ConnectAsync(string url, string version, Action<byte[]> onEvent,
        Action<Exception>? onFailure, Action<ConnectionState>? onStateChange,
        Action<System.Net.WebSockets.ClientWebSocketOptions>? onRequestConstructed,
        System.Threading.CancellationToken cancellationToken = default)
    {
        cancellationToken.ThrowIfCancellationRequested();
        onStateChange?.Invoke(ConnectionState.Connecting);
        Uri uri;
        try
        {
            var target = new UriBuilder(url);
            if (target.Scheme != "ws" && target.Scheme != "wss")
                throw new ArgumentException("Latch URL must use ws or wss", nameof(url));
            var parts = target.Query.TrimStart('?').Split('&', StringSplitOptions.RemoveEmptyEntries)
                .Where(part => Uri.UnescapeDataString(part.Split('=')[0].Replace("+", " ")) != "version");
            target.Query = string.Join("&", parts.Append("version=" + Uri.EscapeDataString(version)));
            uri = target.Uri;
        }
        catch
        {
            try { onStateChange?.Invoke(ConnectionState.Offline); } catch { }
            throw;
        }
        var transport = new LatchTransport(uri, onEvent, onFailure, onStateChange, onRequestConstructed);
        // The token governs only initial connection. After success, the connected client
        // owns its transport and must be disposed explicitly.
        using var registration = cancellationToken.Register(() =>
        {
            if (transport._initial.TrySetCanceled(cancellationToken))
                _ = transport.DisposeAsync();
        });
        if (!cancellationToken.IsCancellationRequested) _ = transport.RunAsync();
        return await transport._initial.Task.ConfigureAwait(false);
    }

    internal Task<byte[]> CallAsync(string method, byte[] payload)
    {
        lock (_gate)
        {
            if (_closed) return Task.FromException<byte[]>(new LatchError("connection_closed", "connection closed"));
            var id = System.Threading.Interlocked.Increment(ref _nextID).ToString(System.Globalization.CultureInfo.InvariantCulture);
            var waiter = new TaskCompletionSource<byte[]>(TaskCreationOptions.RunContinuationsAsynchronously);
            var frame = new LatchEnvelope { Kind = 3, ID = id, Method = method, Payload = payload }.Encode();
            _queued.Enqueue((id, frame, waiter));
            if (_socket != null) _ = FlushAsync(_socket);
            return waiter.Task;
        }
    }

    private async Task FlushAsync(System.Net.WebSockets.ClientWebSocket socket)
    {
        try
        {
            await _sendLock.WaitAsync(_stop.Token).ConfigureAwait(false);
            try
            {
                while (true)
                {
                    (string ID, byte[] Frame, TaskCompletionSource<byte[]> Waiter) request;
                    lock (_gate)
                    {
                        if (_socket != socket || _closed || _queued.Count == 0) return;
                        request = _queued.Dequeue();
                        // An attempted send is ambiguous even if it throws; never replay it.
                        _pending.Add(request.ID, request.Waiter);
                    }
                    await socket.SendAsync(new ArraySegment<byte>(request.Frame), System.Net.WebSockets.WebSocketMessageType.Binary, true, _stop.Token).ConfigureAwait(false);
                }
            }
            finally { _sendLock.Release(); }
        }
        catch (OperationCanceledException) when (_stop.IsCancellationRequested) { }
        catch (Exception error) { Fail(socket, error); }
    }

    private async Task RunAsync()
    {
        bool first = true;
        while (true)
        {
            lock (_gate) { if (_closed) return; _state = ConnectionState.Connecting; }
            if (first) first = false;
            else Notify(ConnectionState.Connecting);
            using var socket = new System.Net.WebSockets.ClientWebSocket();
            lock (_gate) { if (_closed) return; _attempt = socket; }
            try
            {
                _stop.Token.ThrowIfCancellationRequested();
                _onRequestConstructed?.Invoke(socket.Options);
                _stop.Token.ThrowIfCancellationRequested();
                await socket.ConnectAsync(_uri, _stop.Token).ConfigureAwait(false);
                lock (_gate)
                {
                    if (_closed) return;
                    _socket = socket;
                    _state = ConnectionState.Connected;
                }
                Notify(ConnectionState.Connected);
                _initial.TrySetResult(this);
                _ = FlushAsync(socket);
                await ReadLoopAsync(socket).ConfigureAwait(false);
            }
            catch (OperationCanceledException) when (_stop.IsCancellationRequested) { return; }
            catch (Exception error) { Fail(socket, error); }
            lock (_gate) { if (_attempt == socket) _attempt = null; if (_closed) return; }
            try { await Task.Delay(TimeSpan.FromSeconds(2), _stop.Token).ConfigureAwait(false); }
            catch (OperationCanceledException) { return; }
        }
    }

    private async Task ReadLoopAsync(System.Net.WebSockets.ClientWebSocket socket)
    {
        var chunk = new byte[8192];
        while (!_stop.IsCancellationRequested)
        {
            using var message = new System.IO.MemoryStream();
            System.Net.WebSockets.WebSocketReceiveResult result;
            do
            {
                result = await socket.ReceiveAsync(new ArraySegment<byte>(chunk), _stop.Token).ConfigureAwait(false);
                if (result.MessageType == System.Net.WebSockets.WebSocketMessageType.Close)
                    throw new LatchError("connection_closed", "connection closed: " + socket.CloseStatus + " " + socket.CloseStatusDescription);
                if (result.MessageType != System.Net.WebSockets.WebSocketMessageType.Binary)
                    throw new FormatException("Text WebSocket frame received");
                if (result.Count > MaxFrame - message.Length) throw new FormatException("Latch frame too large");
                message.Write(chunk, 0, result.Count);
            } while (!result.EndOfMessage);
            Dispatch(socket, LatchEnvelope.Decode(message.ToArray()));
        }
    }

    private void Dispatch(System.Net.WebSockets.ClientWebSocket socket, LatchEnvelope envelope)
    {
        lock (_gate) { if (_socket != socket || _closed) return; }
        switch (envelope.Kind)
        {
            case 4:
            case 5:
                TaskCompletionSource<byte[]>? waiter;
                lock (_gate) { _pending.TryGetValue(envelope.ID, out waiter); _pending.Remove(envelope.ID); }
                if (envelope.Kind == 4) waiter?.TrySetResult(envelope.Payload);
                else waiter?.TrySetException(new LatchError(envelope.Code, envelope.Error));
                break;
            case 6:
                try { _onEvent(envelope.Payload); } catch (Exception error) { Fail(socket, error); }
                break;
            case 7: Fail(socket, new LatchError(envelope.Code, envelope.Error)); break;
            default: Fail(socket, new FormatException("Unexpected Latch envelope kind")); break;
        }
    }
    private void Notify(ConnectionState state) { try { _onStateChange?.Invoke(state); } catch (Exception) { } }
    private void Fail(System.Net.WebSockets.ClientWebSocket socket, Exception error)
    {
        TaskCompletionSource<byte[]>[] pending;
        lock (_gate)
        {
            if (_closed || _state == ConnectionState.Offline || _attempt != socket) return;
            _socket = null;
            _state = ConnectionState.Offline;
            pending = _pending.Values.ToArray();
            _pending.Clear();
        }
        socket.Abort();
        foreach (var waiter in pending) waiter.TrySetException(error);
        Notify(ConnectionState.Offline);
        try { _onFailure?.Invoke(error); } catch (Exception) { }
    }
    public ValueTask DisposeAsync()
    {
        TaskCompletionSource<byte[]>[] waiters;
        System.Net.WebSockets.ClientWebSocket? socket;
        lock (_gate)
        {
            if (_closed) return default;
            _closed = true;
            socket = _attempt;
            _socket = null;
            _attempt = null;
            waiters = _pending.Values.Concat(_queued.Select(q => q.Waiter)).ToArray();
            _pending.Clear(); _queued.Clear();
        }
        var error = new LatchError("connection_closed", "connection closed");
        _stop.Cancel();
        socket?.Abort();
        _initial.TrySetException(error);
        foreach (var waiter in waiters) waiter.TrySetException(error);
        Notify(ConnectionState.Offline);
        return default;
    }
}
