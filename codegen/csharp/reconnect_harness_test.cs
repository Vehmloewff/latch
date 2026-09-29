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
        await using var client = await new LatchClient(args.Single(), packet => { if (packet.Text == "event") eventReceived.TrySetResult(true); }, states.Enqueue)
            .ConnectAsync(initial.Token).WaitAsync(TimeSpan.FromSeconds(12));
        initial.Cancel(); // cancellation after connection must not close the usable transport
        var sent = client.ChatSendMessageAsync(Request("sent"));
        try { await sent.WaitAsync(TimeSpan.FromSeconds(8)); throw new Exception("sent call survived disconnect"); }
        catch (LatchError error) { if (error.Code != "connection_closed") throw; }
        var offline = client.ChatSendMessageAsync(Request("offline"));
        if ((await offline.WaitAsync(TimeSpan.FromSeconds(12))).Text != "offline") throw new Exception("queued call not flushed");
        await eventReceived.Task.WaitAsync(TimeSpan.FromSeconds(8));
        if (!states.Contains(ConnectionState.Offline) || states.Count(s => s == ConnectionState.Connected) != 2 || states.Count(s => s == ConnectionState.Connecting) < 3)
            throw new Exception("incorrect reconnect states: " + string.Join(",", states));
        Console.WriteLine("C# reconnect passed");
    }
}
