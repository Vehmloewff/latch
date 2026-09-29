using System;
using System.Collections.Generic;
using System.Linq;
using System.Threading.Tasks;

static class Program
{
    static void Check(bool value, string message) { if (!value) throw new Exception(message); }
    static async Task<T> Bounded<T>(Task<T> task) => await task.WaitAsync(TimeSpan.FromSeconds(8));
    static Packet Packet(string text) => new Packet
    {
        Text = text, State = State.Open, Blob = new byte[] { 1, 2 }, History = new(), Tags = new() { ["from"] = "client" }
    };
    static async Task Main(string[] args)
    {
        var eventReceived = new TaskCompletionSource<Packet>(TaskCreationOptions.RunContinuationsAsynchronously);
        var failure = new TaskCompletionSource<Exception>(TaskCreationOptions.RunContinuationsAsynchronously);
        var states = new List<ConnectionState>();
        await using var client = await Bounded(new LatchClient(args.Single(),
            packet => eventReceived.TrySetResult(packet), state => { lock (states) states.Add(state); },
            error => failure.TrySetResult(error)).ConnectAsync());
        lock (states) Check(states.SequenceEqual(new[] { ConnectionState.Connecting, ConnectionState.Connected }), "connect states");
        var one = client.ChatSendMessageAsync(Packet("one"));
        var two = client.ChatSendMessageAsync(Packet("two"));
        var evt = await Bounded(eventReceived.Task);
        Check(evt.Text == "event" && evt.Tags["from"] == "go", "Go event payload");
        var results = await Bounded(Task.WhenAll(one, two));
        Check(results[0].Text == "one" && results[1].Text == "two", "out-of-order response correlation");
        Check(results.All(p => p.Blob.SequenceEqual(new byte[] { 1, 2 }) && p.State == State.Open), "response DTO");
        try { await Bounded(client.ChatSendMessageAsync(Packet("error"))); throw new Exception("RPC error succeeded"); }
        catch (LatchError error) { Check(error.Code == "denied" && error.Message == "not allowed", "RPC error code/message"); }
        var pending = client.ChatSendMessageAsync(Packet("pending"));
        try { await Bounded(pending); throw new Exception("pending RPC survived close"); }
        catch (LatchError error) { Check(error.Code == "connection_closed", "pending close code"); }
        Check((await Bounded(failure.Task)) is LatchError, "failure callback");
        lock (states) Check(states.SequenceEqual(new[] { ConnectionState.Connecting, ConnectionState.Connected, ConnectionState.Offline }), "close states");
        var offline = client.ChatSendMessageAsync(Packet("offline"));
        await client.DisposeAsync();
        try { await Bounded(offline); throw new Exception("queued request survived dispose"); }
        catch (LatchError error) { Check(error.Code == "connection_closed", "queued close code"); }
        try { await Bounded(client.ChatSendMessageAsync(Packet("after close"))); throw new Exception("disposed connection sent request"); }
        catch (LatchError error) { Check(error.Code == "connection_closed", "closed call code"); }
        var failedStates = new List<ConnectionState>();
        try { await new LatchClient("not a websocket URL", _ => { }, state => failedStates.Add(state)).ConnectAsync(); throw new Exception("invalid URL connected"); }
        catch (UriFormatException) { }
        Check(failedStates.SequenceEqual(new[] { ConnectionState.Connecting, ConnectionState.Offline }), "failed connect states");
        Console.WriteLine("C# WebSocket lifecycle passed");
    }
}
