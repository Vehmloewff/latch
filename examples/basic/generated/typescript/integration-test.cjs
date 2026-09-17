// Hand-written (not generated) integration test for the generated
// TypeScript client. Run against a live "basic" example server after
// building with `tsc -p tsconfig.build.json` (see dist/). The server's
// WebSocket URL comes from LATCHWIRE_WS_URL so this can run against
// either the standalone example server or an ephemeral httptest server
// spun up by tests/integration/typescript_test.go.
const { BasicClient, LatchwireError } = require("./dist/index");

const url = process.env.LATCHWIRE_WS_URL || "ws://127.0.0.1:8080/ws";

const watchdog = setTimeout(() => {
  console.error("FAIL: watchdog timeout, test hung");
  process.exit(1);
}, 10000);
watchdog.unref?.();

async function main() {
  const client = new BasicClient({ url });
  const conn = await client.connect({ token: "secret" });

  const gotMessage = new Promise((resolve) => {
    conn.events.messageReceived.subscribe((message) => {
      resolve(message);
    });
  });
  const gotPresence = new Promise((resolve) => {
    conn.events.presenceChanged.subscribe((event) => {
      resolve(event);
    });
  });

  const welcome = await gotMessage;
  console.log("step: got welcome event", welcome);
  if (welcome.room !== "lobby" || welcome.text !== "welcome") {
    throw new Error("unexpected welcome event: " + JSON.stringify(welcome));
  }

  const presence = await gotPresence;
  console.log("step: got presence event", presence);
  if (presence.userId !== "self" || presence.online !== true) {
    throw new Error("unexpected presence event: " + JSON.stringify(presence));
  }

  const result = await conn.room.subscribe({ room: "general" });
  console.log("step: room.subscribe resolved", result);
  if (result.ok !== true) {
    throw new Error("expected ok=true, got " + JSON.stringify(result));
  }

  const listed = await conn.room.list({});
  console.log("step: room.list resolved", listed);
  if (!Array.isArray(listed.rooms) || listed.rooms.length !== 3) {
    throw new Error("unexpected room.list response: " + JSON.stringify(listed));
  }

  // Nested types + optional/nullable fields.
  const profile = await conn.profile.get({ userId: "alice" });
  console.log("step: profile.get resolved", profile);
  if (profile.profile.name !== "User alice" || profile.profile.address.city !== "Springfield") {
    throw new Error("unexpected profile: " + JSON.stringify(profile));
  }
  if (profile.profile.nickname === null || profile.profile.address.zip === null) {
    throw new Error("expected nickname and zip to be present: " + JSON.stringify(profile));
  }

  const noZipProfile = await conn.profile.get({ userId: "no-zip" });
  // Go's `omitempty` on a nil pointer omits the key entirely rather than
  // sending JSON null, so the field is absent (undefined), not null.
  if (noZipProfile.profile.address.zip != null) {
    throw new Error("expected zip to be absent: " + JSON.stringify(noZipProfile));
  }

  // Application error.
  let appErrorOk = false;
  try {
    await conn.profile.get({ userId: "missing" });
  } catch (err) {
    appErrorOk = err instanceof LatchwireError && err.code === "not_found";
  }
  if (!appErrorOk) {
    throw new Error("expected profile.get({userId:'missing'}) to reject with not_found");
  }

  // Concurrent calls: fire many requests at once and verify every response
  // matches its own request despite handlers executing concurrently.
  const ids = Array.from({ length: 10 }, (_, i) => `user-${i}`);
  const profiles = await Promise.all(ids.map((id) => conn.profile.get({ userId: id })));
  profiles.forEach((p, i) => {
    if (p.profile.name !== `User user-${i}`) {
      throw new Error(`concurrent call ${i} returned wrong profile: ${JSON.stringify(p)}`);
    }
  });
  console.log("step: concurrent profile.get calls all resolved correctly");

  let rejectedOk = false;
  try {
    const badClient = new BasicClient({ url });
    await badClient.connect({ token: "" });
  } catch (err) {
    console.log("step: bad connect rejected with code", err && err.code);
    rejectedOk = err instanceof LatchwireError && err.code === "invalid_connect_payload";
  }
  if (!rejectedOk) {
    throw new Error("expected empty token to be rejected with invalid_connect_payload");
  }

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
