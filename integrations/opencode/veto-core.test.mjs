import assert from "node:assert/strict"
import test from "node:test"
import {
  ROUTING_MARKER_VALUE,
  objectiveFromParts,
  parseRouteDecision,
  routeArgs,
  sessionInfo,
  shouldRoute,
} from "./assets/lib/veto-core.js"

test("extracts only real bounded user text", () => {
  assert.equal(objectiveFromParts([
    { type: "text", text: " do work " },
    { type: "text", text: "internal", synthetic: true },
    { type: "file", text: "secret" },
  ]), "do work")
  assert.equal(objectiveFromParts([{ type: "text", text: "x".repeat(17000) }]), "")
})

test("bypasses recursion, internal sessions, continuations, and session opt-out", () => {
  const parts = [{ type: "text", text: "work" }]
  assert.equal(shouldRoute({ parts, enabled: true }), true)
  assert.equal(shouldRoute({ parts, enabled: true, marker: ROUTING_MARKER_VALUE }), false)
  assert.equal(shouldRoute({ parts, enabled: true, internal: true }), false)
  assert.equal(shouldRoute({ parts, enabled: false }), false)
  assert.equal(shouldRoute({ parts: [{ ...parts[0], synthetic: true }], enabled: true }), false)
  assert.equal(shouldRoute({ parts, enabled: false, explicit: "explicit" }), true)
})

test("recognizes Veto-owned sessions by stable title prefix", () => {
  assert.deepEqual(sessionInfo({ type: "session.created", properties: { info: { id: "s1", title: "veto:admission:abc" } } }), { id: "s1", internal: true })
  assert.deepEqual(sessionInfo({ type: "session.deleted", properties: { info: { id: "s2", title: "normal" } } }), { id: "s2", internal: false })
})

test("accepts only exact OpenCode routing identity", () => {
  assert.equal(parseRouteDecision('{"runtime":"opencode","provider":"openai","api_model":"gpt-5"}').api_model, "gpt-5")
  assert.throws(() => parseRouteDecision('{"runtime":"openai-api","provider":"openai","api_model":"gpt-5"}'))
  assert.throws(() => parseRouteDecision('{"runtime":"opencode","provider":"bad id","api_model":"gpt-5"}'))
  assert.throws(() => parseRouteDecision('{"runtime":"opencode","provider":"openai","api_model":"bad model"}'))
})

test("uses an argument array with bounded runtime and timeout", () => {
  assert.deepEqual(routeArgs("--auto is task data", 8000), [
    "route", "--json", "--no-resume", "--runtime", "opencode", "--timeout", "8s", "--", "--auto is task data",
  ])
  assert.equal(routeArgs("task", 1)[6], "1s")
  assert.equal(routeArgs("task", 60000)[6], "30s")
})

test("plugin source never enables unsafe OpenCode approval flags", async () => {
  const source = await import("node:fs/promises").then((fs) => fs.readFile(new URL("./assets/plugins/veto.js", import.meta.url), "utf8"))
  assert.equal(source.includes("--auto"), false)
  assert.equal(source.includes("--yolo"), false)
})
