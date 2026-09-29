using System;
using System.Linq;
using System.Threading.Tasks;

static class Program
{
    static async Task Main(string[] args)
    {
        var client = await new LatchClient(args.Single()).ConnectAsync().WaitAsync(TimeSpan.FromSeconds(8));
        try
        {
            var received = new TaskCompletionSource<Packet>(TaskCreationOptions.RunContinuationsAsynchronously);
            client.OnEvent = packet => received.TrySetResult(packet);
            var request = new Packet { Text = "pending", State = State.Open, Blob = new byte[] { 1 }, History = new(), Tags = new() };
            var pending = client.ChatSendMessageAsync(request);
            // The server sends this event only after reading the request; it
            // deliberately never responds to the pending RPC.
            if ((await received.Task.WaitAsync(TimeSpan.FromSeconds(8))).Text != "received")
                throw new Exception("server did not receive pending request");
            await client.DisposeAsync();
            try { await pending.WaitAsync(TimeSpan.FromSeconds(8)); throw new Exception("pending RPC survived DisposeAsync"); }
            catch (LatchError error)
            {
                if (error.Code != "connection_closed") throw;
            }
            try { await client.ChatSendMessageAsync(request).WaitAsync(TimeSpan.FromSeconds(8)); throw new Exception("disposed client accepted request"); }
            catch (LatchError error)
            {
                if (error.Code != "connection_closed") throw;
            }
            Console.WriteLine("C# pending DisposeAsync passed");
        }
        finally { await client.DisposeAsync(); }
    }
}
