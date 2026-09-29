using System;
using System.Linq;
using System.Threading.Tasks;

static class Program
{
    static Packet Request() => new Packet { Text = "waiting", State = State.Open, Blob = new byte[] { 1 }, History = new(), Tags = new() };
    static async Task Main(string[] args)
    {
        var failure = new TaskCompletionSource<Exception>(TaskCreationOptions.RunContinuationsAsynchronously);
        await using var client = await new LatchClient(args.Single(), _ => { }, onEventError: error => failure.TrySetResult(error)).ConnectAsync().WaitAsync(TimeSpan.FromSeconds(8));
        try { await client.ChatSendMessageAsync(Request()).WaitAsync(TimeSpan.FromSeconds(8)); throw new Exception("connection error did not fail pending call"); }
        catch (LatchError error)
        {
            if (error.Code != "maintenance" || error.Message != "try later") throw;
        }
        if (await failure.Task.WaitAsync(TimeSpan.FromSeconds(8)) is not LatchError { Code: "maintenance" })
            throw new Exception("missing connection failure callback");
        var queued = client.ChatSendMessageAsync(Request());
        await client.DisposeAsync();
        try { await queued.WaitAsync(TimeSpan.FromSeconds(8)); throw new Exception("queued call survived disposal"); }
        catch (LatchError error)
        {
            if (error.Code != "connection_closed") throw;
        }
        Console.WriteLine("C# connection error checks passed");
    }
}
