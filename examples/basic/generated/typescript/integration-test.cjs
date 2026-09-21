// Hand-written (not generated) integration test for the generated
// TypeScript client. Run against a live "basic" example server after
// building with `tsc -p tsconfig.build.json` (see dist/). The server's
// WebSocket URL comes from LATCHWIRE_WS_URL so this can run against
// either the standalone example server or an ephemeral httptest server
// spun up by tests/integration/typescript_test.go.
const { LatchClient, LatchError } = require("./dist/index");

const url = process.env.LATCHWIRE_WS_URL || "ws://127.0.0.1:8080/ws";

const watchdog = setTimeout(() => {
  console.error("FAIL: watchdog timeout, test hung");
  process.exit(1);
}, 10000);
watchdog.unref?.();

async function main() {
  const client = new LatchClient({ url });
  const conn = await client.connect();

  const events = [];
  const gotEvents = new Promise((resolve) => {
    conn.events.subscribe((event) => {
      events.push(event);
      if (events.length === 2) resolve(events);
    });
  });

  const [welcomeEvent, presenceEvent] = await gotEvents;
  const welcome = welcomeEvent.message;
  console.log("step: got welcome event", welcome);
  if (welcome.room !== "lobby" || welcome.text !== "welcome") {
    throw new Error("unexpected welcome event: " + JSON.stringify(welcome));
  }

  const presence = presenceEvent.presence;
  console.log("step: got presence event", presence);
  if (presence.userId !== "self" || presence.online !== true) {
    throw new Error("unexpected presence event: " + JSON.stringify(presence));
  }

  const result = await conn.roomSubscribe({ room: "general" });
  console.log("step: roomSubscribe resolved", result);
  if (result.ok !== true) {
    throw new Error("expected ok=true, got " + JSON.stringify(result));
  }

  const listed = await conn.roomList({});
  console.log("step: roomList resolved", listed);
  if (!Array.isArray(listed.rooms) || listed.rooms.length !== 3) {
    throw new Error("unexpected room.list response: " + JSON.stringify(listed));
  }

  // Nested types + optional/nullable fields.
  const profile = await conn.profileGet({ userId: "alice" });
  console.log("step: profileGet resolved", profile);
  if (profile.profile.name !== "User alice" || profile.profile.address.city !== "Springfield") {
    throw new Error("unexpected profile: " + JSON.stringify(profile));
  }
  if (profile.profile.nickname === null || profile.profile.address.zip === null) {
    throw new Error("expected nickname and zip to be present: " + JSON.stringify(profile));
  }

  const noZipProfile = await conn.profileGet({ userId: "no-zip" });
  // Go's `omitempty` on a nil pointer omits the key entirely rather than
  // sending JSON null, so the field is absent (undefined), not null.
  if (noZipProfile.profile.address.zip != null) {
    throw new Error("expected zip to be absent: " + JSON.stringify(noZipProfile));
  }

  // Application error.
  let appErrorOk = false;
  try {
    await conn.profileGet({ userId: "missing" });
  } catch (err) {
    appErrorOk = err instanceof LatchError && err.code === "not_found";
  }
  if (!appErrorOk) {
    throw new Error("expected profile.get({userId:'missing'}) to reject with not_found");
  }

  // Concurrent calls: fire many requests at once and verify every response
  // matches its own request despite handlers executing concurrently.
  const ids = Array.from({ length: 10 }, (_, i) => `user-${i}`);
  const profiles = await Promise.all(ids.map((id) => conn.profileGet({ userId: id })));
  profiles.forEach((p, i) => {
    if (p.profile.name !== `User user-${i}`) {
      throw new Error(`concurrent call ${i} returned wrong profile: ${JSON.stringify(p)}`);
    }
  });
  console.log("step: concurrent profile.get calls all resolved correctly");

  conn.close();
  console.log("OK: TypeScript generated client integration test passed");
}

main()
  .then(() => {
    clearTimeout(watchdog);
    process.exit(0);
  })
  .catch((err) => {
    clearTimeout(watchdog);
    console.error("FAIL:", err);
    process.exit(1);
  });
