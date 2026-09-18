// Not part of generated output: a hand-written usage smoke test for the
// generated client's static typing, matching the "expected developer
// experience" walkthrough in the README / spec section 44.
import { LatchwireClient } from "./index";

async function main() {
  const client = new LatchwireClient({ url: "ws://localhost:8080/ws" });

  const conn = await client.connect();

  const result = await conn.roomSubscribe({ room: "general" });
  console.log(result.ok);

  const unsubscribe = conn.events.subscribe((event) => {
    console.log(event.kind);
    console.log(event.message?.text);
  });

  unsubscribe();
  conn.close();
}

void main();
