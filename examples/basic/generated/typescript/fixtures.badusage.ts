// Type-safety fixtures (spec section 48): every statement in this file
// must fail to compile. Not named *.ts so it's excluded from the normal
// tsc run; the verification script below renames it in temporarily.
import { BasicClient } from "./index";

async function shouldFailToCompile(client: BasicClient) {
  const conn = await client.connect({ token: "secret" });

  // @ts-expect-error request field has the wrong type (number, not string)
  await conn.room.subscribe({ room: 123 });

  // @ts-expect-error unknown request field
  await conn.room.subscribe({ room: "general", extra: true });

  const result = await conn.room.subscribe({ room: "general" });
  // @ts-expect-error SubscribeResponse has no "nonexistent" field
  console.log(result.nonexistent);

  conn.events.messageReceived.subscribe((event) => {
    // @ts-expect-error MessageReceived has no "nonexistent" field
    console.log(event.nonexistent);
  });

  // @ts-expect-error connect() requires a ConnectParams payload
  await client.connect({});

  // @ts-expect-error unknown top-level method namespace
  await conn.doesNotExist.get({});
}
