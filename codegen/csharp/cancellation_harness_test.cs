using System;
using System.Collections.Concurrent;
using System.Linq;
using System.Threading;
using System.Threading.Tasks;

static class Program
{
    static async Task Canceled(Task task)
    {
        try { await task.WaitAsync(TimeSpan.FromSeconds(6)); throw new Exception("initial connect succeeded after cancellation"); }
        catch (OperationCanceledException) { }
    }

    static async Task Main(string[] args)
    {
        using var before = new CancellationTokenSource();
        before.Cancel();
        await Canceled(new LatchClient(args[0], _ => { }).ConnectAsync(before.Token));

        var states = new ConcurrentQueue<ConnectionState>();
        var failed = new TaskCompletionSource<bool>(TaskCreationOptions.RunContinuationsAsynchronously);
        using var duringDelay = new CancellationTokenSource();
        var retrying = new LatchClient(args[0], _ => { }, states.Enqueue, _ => failed.TrySetResult(true))
            .ConnectAsync(duringDelay.Token);
        await failed.Task.WaitAsync(TimeSpan.FromSeconds(6));
        duringDelay.Cancel();
        await Canceled(retrying);
        if (!states.Contains(ConnectionState.Offline)) throw new Exception("missing offline state");
        await Task.Delay(TimeSpan.FromMilliseconds(2400)); // longer than one retry interval

        using var duringHandshake = new CancellationTokenSource(TimeSpan.FromSeconds(2));
        await Canceled(new LatchClient(args[1], _ => { }).ConnectAsync(duringHandshake.Token));
        Console.WriteLine("C# initial connection cancellation passed");
    }
}
