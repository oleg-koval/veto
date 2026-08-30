import assert from "node:assert/strict"
import { mkdtemp, mkdir, readFile, writeFile } from "node:fs/promises"
import { tmpdir } from "node:os"
import { join } from "node:path"
import test from "node:test"
import { pathToFileURL } from "node:url"

async function loadPlugin(t, decisions = []) {
  const root = await mkdtemp(join(tmpdir(), "veto-opencode-plugin-"))
  t.after(async () => { delete globalThis.Bun; delete process.env.VETO_ROUTING_ORIGIN })
  await mkdir(join(root, "plugins"), { recursive: true })
  await mkdir(join(root, "veto"), { recursive: true })
  await mkdir(join(root, "node_modules", "@opencode-ai", "plugin"), { recursive: true })
  await writeFile(join(root, "plugins", "veto.js"), await readFile(new URL("./assets/plugins/veto.js", import.meta.url)))
  await writeFile(join(root, "veto", "veto-core.js"), await readFile(new URL("./assets/lib/veto-core.js", import.meta.url)))
  await writeFile(join(root, "package.json"), '{"type":"module"}')
  await writeFile(join(root, "node_modules", "@opencode-ai", "plugin", "package.json"), '{"type":"module","exports":"./index.js"}')
  await writeFile(join(root, "node_modules", "@opencode-ai", "plugin", "index.js"), `
    export function tool(value) { return value }
    const schema = { min() { return this }, max() { return this } }
    tool.schema = { string() { return Object.create(schema) } }
  `)

  const calls = []
  globalThis.Bun = {
    spawn(argv, options) {
      calls.push({ argv, options })
      if (argv[1] === "version") {
        return { stdout: textStream("veto 0.3.0\n"), stderr: textStream(""), exited: Promise.resolve(0), kill() {} }
      }
      const decision = decisions.shift() || { runtime: "opencode", provider: "openai", api_model: "gpt-5" }
      return {
        stdout: textStream(JSON.stringify(decision)),
        stderr: textStream(""),
        exited: Promise.resolve(0),
        kill() {},
      }
    },
  }
  const toasts = []
  const module = await import(pathToFileURL(join(root, "plugins", "veto.js")).href + `?test=${Math.random()}`)
  const hooks = await module.VetoPlugin({
    directory: "/safe/project",
    client: { tui: { showToast: async (input) => { toasts.push(input.body) } } },
  })
  return { hooks, calls, toasts }
}

function textStream(text) {
  const bytes = new TextEncoder().encode(text)
  return new ReadableStream({ start(controller) { controller.enqueue(bytes); controller.close() } })
}

test("routes a user turn and exposes explicit status and route tools", async (t) => {
  const { hooks, calls, toasts } = await loadPlugin(t)
  const output = { message: {}, parts: [{ type: "text", text: "Build the UI" }] }
  await hooks["chat.message"]({ sessionID: "user-1" }, output)
  assert.deepEqual(output.message.model, { providerID: "openai", modelID: "gpt-5" })
  assert.deepEqual(calls[0].argv.slice(0, 8), ["veto", "route", "--json", "--no-resume", "--runtime", "opencode", "--timeout", "8s"])
  assert.equal(calls[0].options.env.VETO_ROUTING_ORIGIN, "opencode-plugin")
  assert.equal(calls[0].options.cwd, "/safe/project")
  assert.match(toasts[0].message, /Routed to openai\/gpt-5/)

  const status = JSON.parse(await hooks.tool.veto_status.execute({}, { sessionID: "user-1" }))
  assert.equal(status.available, true)
  assert.equal(status.version, "0.3.0")
  assert.equal(status.automatic_routing, true)
  const decision = JSON.parse(await hooks.tool.veto_route.execute({ objective: "Inspect only" }, { sessionID: "user-1", abort: new AbortController().signal }))
  assert.equal(decision.runtime, "opencode")
})

test("session commands are visible and explicit route overrides opt-out", async (t) => {
  const { hooks, calls } = await loadPlugin(t)
  const off = { parts: [{ type: "text", text: "off" }] }
  await hooks["command.execute.before"]({ command: "veto-off", sessionID: "s1", arguments: "" }, off)
  assert.equal(off.parts[0].synthetic, true)
  await hooks["chat.message"]({ sessionID: "s1" }, { message: {}, parts: [{ type: "text", text: "ordinary" }] })
  assert.equal(calls.length, 0)

  const command = { parts: [{ type: "text", text: "Explicit task" }] }
  await hooks["command.execute.before"]({ command: "veto-route", sessionID: "s1", arguments: "Explicit task" }, command)
  const output = { message: {}, parts: command.parts }
  await hooks["chat.message"]({ sessionID: "s1" }, output)
  assert.equal(calls.filter((call) => call.argv[1] === "route").length, 1)
  assert.deepEqual(output.message.model, { providerID: "openai", modelID: "gpt-5" })
})

test("global default-off can be visibly overridden for one session", async (t) => {
  process.env.VETO_OPENCODE_AUTO = "0"
  t.after(() => { delete process.env.VETO_OPENCODE_AUTO })
  const { hooks, calls } = await loadPlugin(t)
  assert.equal(JSON.parse(await hooks.tool.veto_status.execute({}, { sessionID: "s2" })).automatic_routing, false)
  const command = { parts: [{ type: "text", text: "on" }] }
  await hooks["command.execute.before"]({ command: "veto-on", sessionID: "s2", arguments: "" }, command)
  assert.equal(JSON.parse(await hooks.tool.veto_status.execute({}, { sessionID: "s2" })).automatic_routing, true)
  await hooks["chat.message"]({ sessionID: "s2" }, { message: {}, parts: [{ type: "text", text: "work" }] })
  assert.equal(calls.filter((call) => call.argv[1] === "route").length, 1)
})

test("Veto sessions, inherited recursion markers, and synthetic continuations bypass routing", async (t) => {
  const loaded = await loadPlugin(t)
  await loaded.hooks.event({ event: { type: "session.created", properties: { info: { id: "internal", title: "veto:admission:123" } } } })
  await loaded.hooks["chat.message"]({ sessionID: "internal" }, { message: {}, parts: [{ type: "text", text: "admit" }] })
  await loaded.hooks["chat.message"]({ sessionID: "user" }, { message: {}, parts: [{ type: "text", text: "continue", synthetic: true }] })
  process.env.VETO_ROUTING_ORIGIN = "opencode-plugin"
  await loaded.hooks["chat.message"]({ sessionID: "user" }, { message: {}, parts: [{ type: "text", text: "recursive" }] })
  assert.equal(loaded.calls.length, 0)
})

test("does not intercept or auto-answer OpenCode permissions", async (t) => {
  const { hooks } = await loadPlugin(t)
  assert.equal(hooks["permission.ask"], undefined)
  assert.equal(hooks["tool.execute.before"], undefined)
  assert.equal(hooks["shell.env"], undefined)
})

test("pre-aborted explicit routing never spawns Veto", async (t) => {
  const { hooks, calls } = await loadPlugin(t)
  const controller = new AbortController()
  controller.abort()
  await assert.rejects(() => hooks.tool.veto_route.execute(
    { objective: "Do not start" },
    { sessionID: "s3", abort: controller.signal },
  ), /aborted/)
  assert.equal(calls.length, 0)
})
