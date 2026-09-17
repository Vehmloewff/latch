// Not part of generated output: a hand-written usage smoke test for the
// generated client's static typing, matching the "expected developer
// experience" walkthrough in the README / spec section 44.
import { BasicClient } from "./index";

async function main() {
  const client = new BasicClient({ url: "ws://localhost:8080/ws" });

  const conn = await client.connect({ token: "secret" });

  const result = await conn.room.subscribe({ room: "general" });
  console.log(result.ok);

  const unsubscribe = conn.events.messageReceived.subscribe((message) => {
    console.log(message.room);
    console.log(message.text);
  });

  unsubscribe();
  conn.close();
}

void main();
