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
    private readonly System.Net.WebSockets.ClientWebSocket _socket;
    private readonly System.Threading.CancellationTokenSource _stop = new System.Threading.CancellationTokenSource();
    private readonly System.Threading.SemaphoreSlim _sendLock = new System.Threading.SemaphoreSlim(1, 1);
    private readonly object _gate = new object();
    private readonly Dictionary<string, TaskCompletionSource<byte[]>> _pending = new Dictionary<string, TaskCompletionSource<byte[]>>();
    private readonly Action<byte[]> _onEvent;
    private readonly Action<Exception>? _onFailure;
    private readonly Action<ConnectionState>? _onStateChange;
    private long _nextID;
    private bool _closed;

    private LatchTransport(System.Net.WebSockets.ClientWebSocket socket, Action<byte[]> onEvent,
        Action<Exception>? onFailure, Action<ConnectionState>? onStateChange)
    {
        _socket = socket;
        _onEvent = onEvent;
        _onFailure = onFailure;
        _onStateChange = onStateChange;
    }

    internal static async Task<LatchTransport> ConnectAsync(string url, string version, Action<byte[]> onEvent,
        Action<Exception>? onFailure, Action<ConnectionState>? onStateChange)
    {
        onStateChange?.Invoke(ConnectionState.Connecting);
        try
        {
            var target = new UriBuilder(url);
            if (target.Scheme != "ws" && target.Scheme != "wss")
                throw new ArgumentException("Latch URL must use ws or wss", nameof(url));
            // Preserve unrelated query parameters while replacing any existing version.
            var parts = target.Query.TrimStart('?').Split('&', StringSplitOptions.RemoveEmptyEntries)
                .Where(part => Uri.UnescapeDataString(part.Split('=')[0].Replace("+", " ")) != "version");
            target.Query = string.Join("&", parts.Append("version=" + Uri.EscapeDataString(version)));
            var socket = new System.Net.WebSockets.ClientWebSocket();
            try
            {
                await socket.ConnectAsync(target.Uri, System.Threading.CancellationToken.None).ConfigureAwait(false);
                var transport = new LatchTransport(socket, onEvent, onFailure, onStateChange);
                onStateChange?.Invoke(ConnectionState.Connected);
                _ = transport.ReadLoopAsync();
                return transport;
            }
            catch { socket.Dispose(); throw; }
        }
        catch
        {
            try { onStateChange?.Invoke(ConnectionState.Offline); } catch { }
            throw;
        }
    }

    internal async Task<byte[]> CallAsync(string method, byte[] payload)
    {
        TaskCompletionSource<byte[]> waiter;
        string id;
        lock (_gate)
        {
            if (_closed) throw new LatchError("connection_closed", "connection closed");
            id = System.Threading.Interlocked.Increment(ref _nextID).ToString(System.Globalization.CultureInfo.InvariantCulture);
            waiter = new TaskCompletionSource<byte[]>(TaskCreationOptions.RunContinuationsAsynchronously);
            _pending.Add(id, waiter);
        }
        try
        {
            var frame = new LatchEnvelope { Kind = 3, ID = id, Method = method, Payload = payload }.Encode();
            await _sendLock.WaitAsync(_stop.Token).ConfigureAwait(false);
            try
            {
                if (_stop.IsCancellationRequested) throw new LatchError("connection_closed", "connection closed");
                await _socket.SendAsync(new ArraySegment<byte>(frame), System.Net.WebSockets.WebSocketMessageType.Binary, true, _stop.Token).ConfigureAwait(false);
            }
            finally { _sendLock.Release(); }
        }
        catch (Exception error)
        {
            Fail(error);
        }
        return await waiter.Task.ConfigureAwait(false);
    }

    private async Task ReadLoopAsync()
    {
        var chunk = new byte[8192];
        try
        {
            while (!_stop.IsCancellationRequested)
            {
                using var message = new System.IO.MemoryStream();
                System.Net.WebSockets.WebSocketReceiveResult result;
                do
                {
                    result = await _socket.ReceiveAsync(new ArraySegment<byte>(chunk), _stop.Token).ConfigureAwait(false);
                    if (result.MessageType == System.Net.WebSockets.WebSocketMessageType.Close)
                        throw new LatchError("connection_closed", "connection closed: " + _socket.CloseStatus + " " + _socket.CloseStatusDescription);
                    if (result.MessageType != System.Net.WebSockets.WebSocketMessageType.Binary)
                        throw new FormatException("Text WebSocket frame received");
                    if (result.Count > MaxFrame - message.Length) throw new FormatException("Latch frame too large");
                    message.Write(chunk, 0, result.Count);
                } while (!result.EndOfMessage);
                Dispatch(LatchEnvelope.Decode(message.ToArray()));
            }
        }
        catch (OperationCanceledException) when (_stop.IsCancellationRequested) { }
        catch (Exception error) { Fail(error); }
    }

    private void DeliverEvent(Action<byte[]> handler, byte[] payload)
    {
        try { handler(payload); }
        catch (Exception error) { Fail(error); }
    }
    private void Dispatch(LatchEnvelope envelope)
    {
        switch (envelope.Kind)
        {
            case 4:
            case 5:
                TaskCompletionSource<byte[]>? waiter;
                lock (_gate)
                {
                    _pending.TryGetValue(envelope.ID, out waiter);
                    _pending.Remove(envelope.ID);
                }
                if (envelope.Kind == 4) waiter?.TrySetResult(envelope.Payload);
                else waiter?.TrySetException(new LatchError(envelope.Code, envelope.Error));
                break;
            case 6:
                lock (_gate) { if (_closed) return; }
                DeliverEvent(_onEvent, envelope.Payload);
                break;
            case 7: Fail(new LatchError(envelope.Code, envelope.Error)); break;
            default: Fail(new FormatException("Unexpected Latch envelope kind")); break;
        }
    }
    private void Fail(Exception error)
    {
        TaskCompletionSource<byte[]>[] pending;
        lock (_gate)
        {
            if (_closed) return;
            _closed = true;
            pending = _pending.Values.ToArray();
            _pending.Clear();
        }
        _stop.Cancel();
        _socket.Abort();
        foreach (var waiter in pending) waiter.TrySetException(error);
        try { _onStateChange?.Invoke(ConnectionState.Offline); } catch (Exception) { /* The connection is already closed. */ }
        try { _onFailure?.Invoke(error); } catch (Exception) { /* A failed callback cannot revive the connection. */ }
    }
    public ValueTask DisposeAsync()
    {
        Fail(new LatchError("connection_closed", "connection closed"));
        _socket.Dispose();
        return default;
    }
}
