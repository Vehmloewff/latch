using System;
using System.Collections.Concurrent;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;

static class Program
{
    static Packet Request(string text) => new Packet { Text = text, State = State.Open, Blob = new byte[] { 1 }, History = new(), Tags = new() };
    static async Task Main(string[] args)
    {
        var states = new ConcurrentQueue<ConnectionState>();
        var eventReceived = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
        using var initial = new CancellationTokenSource();
        var hooks = 0;
        var hookFailures = 0;
        await using var client = await new LatchClient(args.Single(), packet => { if (packet.Text == "event") eventReceived.TrySetResult(true); }, states.Enqueue,
            error => { if (error.Message == "hook failure") Interlocked.Increment(ref hookFailures); },
            options => {
                var attempt = Interlocked.Increment(ref hooks);
                if (attempt == 1) throw new Exception("hook failure");
                options.SetRequestHeader("X-Latch-Attempt", attempt.ToString());
            }).ConnectAsync(initial.Token).WaitAsync(TimeSpan.FromSeconds(16));
        initial.Cancel(); // cancellation after connection must not close the usable transport
        var sent = client.ChatSendMessageAsync(Request("sent"));
        try { await sent.WaitAsync(TimeSpan.FromSeconds(8)); throw new Exception("sent call survived disconnect"); }
        catch (LatchError error) { if (error.Code != "connection_closed") throw; }
        var offline = client.ChatSendMessageAsync(Request("offline"));
        if ((await offline.WaitAsync(TimeSpan.FromSeconds(12))).Text != "offline") throw new Exception("queued call not flushed");
        await eventReceived.Task.WaitAsync(TimeSpan.FromSeconds(8));
        if (hooks != 4 || hookFailures != 1) throw new Exception($"hook attempts/failures: {hooks}/{hookFailures}");
        if (!states.Contains(ConnectionState.Offline) || states.Count(s => s == ConnectionState.Connected) != 2 || states.Count(s => s == ConnectionState.Connecting) < 4)
            throw new Exception("incorrect reconnect states: " + string.Join(",", states));
        Console.WriteLine("C# reconnect passed");
    }
}
