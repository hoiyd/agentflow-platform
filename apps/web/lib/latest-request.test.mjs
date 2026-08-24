import assert from "node:assert/strict";
import test from "node:test";

import { createLatestRequestController } from "./latest-request.ts";

function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, resolve, reject };
}

test("only the latest request may commit after responses arrive out of order", async () => {
  const controller = createLatestRequestController();
  const firstResponse = deferred();
  const secondResponse = deferred();
  const committed = [];

  const first = controller.begin();
  const firstLoad = firstResponse.promise.then((value) => {
    if (first.isCurrent()) committed.push(value);
  });

  const second = controller.begin();
  const secondLoad = secondResponse.promise.then((value) => {
    if (second.isCurrent()) committed.push(value);
  });

  secondResponse.resolve("conversation-b");
  await secondLoad;
  firstResponse.resolve("conversation-a");
  await firstLoad;

  assert.equal(first.signal.aborted, true);
  assert.equal(second.signal.aborted, false);
  assert.deepEqual(committed, ["conversation-b"]);
});

test("cancel invalidates the active request even when it later rejects", async () => {
  const controller = createLatestRequestController();
  const response = deferred();
  const request = controller.begin();
  let visibleError = "";

  const load = response.promise.catch((error) => {
    if (request.isCurrent()) visibleError = error.message;
  });

  controller.cancel();
  response.reject(new Error("stale request failed"));
  await load;

  assert.equal(request.signal.aborted, true);
  assert.equal(request.isCurrent(), false);
  assert.equal(visibleError, "");
});
