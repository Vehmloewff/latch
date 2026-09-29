import { LatchClient } from "./client.ts";

const url = process.env.SERVER_URL ?? "ws://127.0.0.1:8080/ws";

// Story: Bob has opened a chat app, and Alice is already chatting in the same
// room. We keep two connections to make the server-to-client event fan-out
// visible in one small walkthrough.
const bobClient = new LatchClient({ url, onEvent: (event) => console.log("Bob received event:", event) });
const aliceClient = new LatchClient({ url, onEvent: (event) => console.log("Alice received event:", event) });

// Bob has opened the chat app and connected to the server.
const bob = await bobClient.connect();
console.log("Bob connected");

// Alice opens her own connection, just as another browser tab or device would.
const alice = await aliceClient.connect();
console.log("Alice connected");


try {
  // Bob asks the server for rooms so the chat app can render its room picker.
  console.log("Bob sees rooms:", (await bob.chatListRooms({})).rooms);

  // Bob joins #general. The server now knows which room should receive his events.
  console.log("Bob joined:", await bob.chatJoinRoom({ room: "general", userId: "bob" }));

  // Alice joins the same room from her independent connection.
  console.log("Alice joined:", await alice.chatJoinRoom({ room: "general", userId: "alice" }));

  // Alice sends a message. The response goes to Alice, while a message event
  // is delivered to both Alice and Bob for their chat timelines.
  console.log("Alice sent:", await alice.chatSendMessage({
    room: "general",
    senderId: "alice",
    text: "Hi Bob, welcome!",
  }));

  // Bob can reload the room timeline from the server instead of depending only
  // on events that may have arrived before his UI was ready.
  console.log("Bob loads history:", await bob.chatHistory({ room: "general" }));
} finally {

  bob.close();
  alice.close();
  console.log("Both chat connections closed");
}
