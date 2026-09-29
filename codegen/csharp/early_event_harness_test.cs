using System;
using System.Linq;
using System.Threading.Tasks;

static class Program
{
    static async Task Main(string[] args)
    {
        await using var client = await new LatchClient(args.Single()).ConnectAsync().WaitAsync(TimeSpan.FromSeconds(8));
        // The server sends the event before it sees this request. Its response is
        // an ordering barrier: the read loop must have dispatched the event first.
        var request = new Packet { Text = "barrier", State = State.Open, Blob = new byte[] { 1 }, History = new(), Tags = new() };
        var response = await client.ChatSendMessageAsync(request).WaitAsync(TimeSpan.FromSeconds(8));
        if (response.Text != "barrier") throw new Exception("request barrier failed");
        var eventReceived = new TaskCompletionSource<Packet>(TaskCreationOptions.RunContinuationsAsynchronously);
        client.OnEvent = value => eventReceived.TrySetResult(value);
        var evt = await eventReceived.Task.WaitAsync(TimeSpan.FromSeconds(3));
        if (evt.Text != "early" || evt.Tags["from"] != "go") throw new Exception("incorrect buffered event");
        Console.WriteLine("C# early event passed");
    }
}
