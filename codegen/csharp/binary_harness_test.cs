// Executed with the freshly generated LatchClient.cs in a temporary net8.0 project.
using System;
using System.Collections.Generic;
using System.Linq;

static class Program
{
    static byte[] Unhex(string hex) => Convert.FromHexString(hex);
    static string Hex(byte[] bytes) => Convert.ToHexString(bytes).ToLowerInvariant();
    static void Check(bool value, string message) { if (!value) throw new Exception(message); }
    static void Bad(string hex)
    {
        try { LatchBinary.Decode(Unhex(hex)); }
        catch (FormatException) { return; }
        throw new Exception("accepted malformed binary value: " + hex);
    }
    static void Reject<T>(Action action) where T : Exception
    {
        try { action(); }
        catch (T) { return; }
        throw new Exception("expected " + typeof(T).Name);
    }
    static void Main()
    {
        var vectors = new Dictionary<string, string> { VECTOR_PAIRS };
        foreach (var (name, hex) in vectors)
        {
            if (name == "envelope_response")
            {
                var envelope = LatchEnvelope.Decode(Unhex(hex));
                Check(envelope.Kind == 4 && envelope.ID == "42" && envelope.Payload.SequenceEqual(new byte[] { 1, 2, 3 }) && envelope.Code == "x", name);
                Check(Hex(envelope.Encode()) == hex, name + " round trip");
            }
            else Check(Hex(LatchBinary.Encode(LatchBinary.Decode(Unhex(hex)))) == hex, name + " round trip");
        }
        Check((long)LatchBinary.Decode(Unhex(vectors["int_minus_one"]))! == -1, "signed fixture");
        Check((ulong)LatchBinary.Decode(Unhex(vectors["uint_300"]))! == 300, "unsigned fixture");
        Check((float)LatchBinary.Decode(Unhex(vectors["float32_one"]))! == 1f, "float32 fixture");
        Check((double)LatchBinary.Decode(Unhex(vectors["float64_one"]))! == 1d, "float64 fixture");
        Check((string)LatchBinary.Decode(Unhex(vectors["string_hello"]))! == "hello", "string fixture");
        Check(((byte[])LatchBinary.Decode(Unhex(vectors["bytes_binary"]))!).SequenceEqual(new byte[] { 0, 255, 127 }), "bytes fixture");
        var nested = LatchValue.AsStruct(LatchBinary.Decode(Unhex(vectors["struct_nested"])));
        Check(LatchValue.AsString(nested[1]) == "hello" && LatchValue.AsSigned(nested[2]) == -19, "nested fixture");
        Check(LatchValue.AsList(nested[3]).Count == 3 && LatchValue.AsBytes(nested[4]).SequenceEqual(new byte[] { 1, 2, 255 }), "nested collection fixture");
        var boundary = Unhex("GO_BOUNDARY");
        var fields = LatchValue.AsStruct(LatchBinary.Decode(boundary));
        Check(LatchValue.AsSigned(fields[7]) == long.MinValue && LatchValue.AsUnsigned(fields[19]) == ulong.MaxValue, "Go integer boundaries");
        Check(Hex(LatchBinary.Encode(fields)) == Hex(boundary), "Go boundary re-encode");
        Check(Hex(LatchBinary.Encode(long.MaxValue)) == "03feffffffffffffffff01", "max signed");
        Check(Hex(LatchBinary.Encode(ulong.MaxValue)) == "04ffffffffffffffffff01", "max unsigned");
        Check(Hex(LatchBinary.Encode(new DateTimeOffset(1969, 12, 31, 23, 59, 59, TimeSpan.Zero))) == "0cffa7d6b907", "pre-epoch time");
        Check((DateTimeOffset)LatchBinary.Decode(LatchBinary.Encode(DateTimeOffset.UnixEpoch.AddTicks(1234567)))! == DateTimeOffset.UnixEpoch.AddTicks(1234567), "time round trip");
        Check(Hex(LatchBinary.Encode(new List<object?> { null, true, "é" })) == "0a0300020702c3a9", "list wire");
        Check(Hex(LatchBinary.Encode(new Dictionary<string, object?> { ["z"] = false, ["a"] = null })) == "0b020701610007017a01", "sorted map wire");
        foreach (var bad in new[] { "", "ff", "038000", "07ff", "0701ff", "09010000", "090207000700", "0b020701610007016100", "030001", "05", "06ffffff", "04ffffffffffffffffffff02", "0a80808008", "09ffffffff1000", "0c80" }) Bad(bad);
        Reject<FormatException>(() => LatchBinary.Encode(new Dictionary<ulong, object?> { [0] = 1L }));
        Reject<FormatException>(() => LatchBinary.Encode(new Dictionary<ulong, object?> { [4294967296] = 1L }));
        Reject<FormatException>(() => Packet.FromLatch(new Dictionary<ulong, object?>()));
        Reject<OverflowException>(() => Metrics.FromLatch(new Dictionary<ulong, object?> { [1] = 128L, [3] = new List<object?> { 1UL, 2UL, 3UL }, [4] = DateTimeOffset.UnixEpoch, [5] = new List<object?>() }));
        var leaf = new Packet { Text = "child", State = State.Closed, Blob = new byte[] { 255 }, History = new(), Tags = new() };
        var packet = new Packet { Text = "parent", State = State.Open, Nullable = null, Optional = null, Blob = new byte[] { 0, 255 }, History = new() { leaf }, Tags = new() { ["é"] = "value" } };
        var serialized = LatchValue.AsStruct(packet.ToLatch());
        Check(serialized.ContainsKey(13) && !serialized.ContainsKey(1) && serialized.ContainsKey(3) && !serialized.ContainsKey(4), "sparse / optional / nullable fields");
        var result = Packet.FromLatch(LatchBinary.Decode(LatchBinary.Encode(packet.ToLatch())));
        Check(result.Text == "parent" && result.State == State.Open && result.Nullable == null && result.Optional == null, "packet fields");
        Check(result.History.Single().Text == "child" && result.History.Single().State == State.Closed && result.Tags["é"] == "value" && result.Blob.SequenceEqual(packet.Blob), "nested DTO");
        packet.Nullable = "present"; packet.Optional = "set";
        result = Packet.FromLatch(LatchBinary.Decode(LatchBinary.Encode(packet.ToLatch())));
        Check(result.Nullable == "present" && result.Optional == "set", "present optional and nullable");
        var metrics = new Metrics { Small = sbyte.MinValue, Count = uint.MaxValue, Fixed = new() { 0, 128, 255 }, When = DateTimeOffset.UnixEpoch.AddTicks(-12345), Samples = new() { null, "x" } };
        var converted = Metrics.FromLatch(LatchBinary.Decode(LatchBinary.Encode(metrics.ToLatch())));
        Check(converted.Small == sbyte.MinValue && converted.Count == uint.MaxValue && converted.Fixed.SequenceEqual(metrics.Fixed), "scalar DTO bounds / fixed array");
        Check(converted.When == metrics.When && converted.Samples.Count == 2 && converted.Samples[0] == null && converted.Samples[1] == "x", "time and nullable list");
        metrics.Fixed = new() { 1, 2 };
        Reject<FormatException>(() => metrics.ToLatch());
        Console.WriteLine("C# binary/DTO checks passed");
    }
}
