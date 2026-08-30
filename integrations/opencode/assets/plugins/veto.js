import { tool } from "@opencode-ai/plugin"
import {
  MAX_OBJECTIVE_BYTES,
  MAX_OUTPUT_BYTES,
  ROUTING_MARKER,
  ROUTING_MARKER_VALUE,
  objectiveFromParts,
  parseRouteDecision,
  routeArgs,
  sessionInfo,
  shouldRoute,
} from "../veto/veto-core.js"

const timeoutMS = boundedNumber(process.env.VETO_OPENCODE_TIMEOUT_MS, 8000, 1000, 30000)

export const VetoPlugin = async ({ client, directory }) => {
  const internalSessions = new Set()
  const sessionModes = new Map()
  const explicitObjectives = new Map()

  const automaticEnabled = (sessionID) =>
    sessionModes.has(sessionID) ? sessionModes.get(sessionID) : process.env.VETO_OPENCODE_AUTO !== "0"

  const toast = async (message, variant = "info") => {
    try {
      await client.tui.showToast({ body: { title: "Veto", message, variant, duration: 5000 } })
    } catch (_) {
      // Headless OpenCode clients do not expose a TUI. Routing still works.
    }
  }

  const runVeto = async (args, signal) => {
    if (signal?.aborted) throw new Error("Veto command aborted")
    const binary = process.env.VETO_BINARY || "veto"
    const env = { ...process.env, [ROUTING_MARKER]: ROUTING_MARKER_VALUE }
    const proc = Bun.spawn([binary, ...args], {
      cwd: directory,
      env,
      stdout: "pipe",
      stderr: "pipe",
    })
    let expired = false
    const stop = () => { try { proc.kill() } catch (_) {} }
    const timer = setTimeout(() => { expired = true; stop() }, timeoutMS)
    const abort = () => stop()
    signal?.addEventListener("abort", abort, { once: true })
    try {
      const [stdout, stderr, exitCode] = await Promise.all([
        readBounded(proc.stdout, MAX_OUTPUT_BYTES),
        readBounded(proc.stderr, MAX_OUTPUT_BYTES),
        proc.exited,
      ])
      if (expired) throw new Error("Veto command timed out")
      if (exitCode !== 0) throw new Error(cleanError(stderr) || `Veto exited with status ${exitCode}`)
      return stdout
    } catch (error) {
      stop()
      throw error
    } finally {
      clearTimeout(timer)
      signal?.removeEventListener("abort", abort)
    }
  }

  const route = async (objective, signal) => {
    if (!objective || new TextEncoder().encode(objective).byteLength > MAX_OBJECTIVE_BYTES) {
      throw new Error("Veto objective must be between 1 and 16384 bytes")
    }
    return parseRouteDecision(await runVeto(routeArgs(objective, timeoutMS), signal))
  }

  const status = async (sessionID, signal) => {
    const result = { available: false, automatic_routing: automaticEnabled(sessionID) }
    try {
      const output = await runVeto(["version"], signal)
      const match = /^veto ([^\s]{1,64})\s*$/.exec(output)
      if (!match) throw new Error("invalid Veto version output")
      return { ...result, available: true, version: match[1] }
    } catch (_) {
      return result
    }
  }

  const setCommandResult = (output, text) => {
    output.parts.splice(0, output.parts.length, { type: "text", text, synthetic: true })
  }

  return {
    event: async ({ event }) => {
      const info = sessionInfo(event)
      if (!info) return
      if (event.type === "session.deleted") {
        internalSessions.delete(info.id)
        sessionModes.delete(info.id)
        explicitObjectives.delete(info.id)
      } else if (info.internal) {
        internalSessions.add(info.id)
      }
    },

    "command.execute.before": async (input, output) => {
      if (input.command === "veto-off") {
        sessionModes.set(input.sessionID, false)
        setCommandResult(output, "Veto automatic routing is OFF for this session.")
        await toast("Automatic routing OFF for this session", "warning")
      } else if (input.command === "veto-on") {
        sessionModes.set(input.sessionID, true)
        setCommandResult(output, "Veto automatic routing is ON for this session.")
        await toast("Automatic routing ON for this session", "success")
      } else if (input.command === "veto-status") {
        const current = await status(input.sessionID)
        const availability = current.available ? `Veto ${current.version}` : "Veto executable unavailable"
        const message = `${availability}; automatic routing is ${current.automatic_routing ? "ON" : "OFF"} for this session.`
        setCommandResult(output, message)
        await toast(message, current.available ? "info" : "warning")
      } else if (input.command === "veto-route") {
        const objective = input.arguments.trim()
        if (!objective || new TextEncoder().encode(objective).byteLength > MAX_OBJECTIVE_BYTES) {
          setCommandResult(output, "Usage: /veto-route <task>")
          await toast("Provide a task up to 16384 bytes after /veto-route", "error")
        } else {
          explicitObjectives.set(input.sessionID, objective)
        }
      }
    },

    "chat.message": async (input, output) => {
      const explicit = explicitObjectives.get(input.sessionID) || ""
      explicitObjectives.delete(input.sessionID)
      const enabled = automaticEnabled(input.sessionID)
      if (!shouldRoute({
        marker: process.env[ROUTING_MARKER],
        internal: internalSessions.has(input.sessionID),
        enabled,
        explicit,
        parts: output.parts,
      })) return
      const objective = explicit || objectiveFromParts(output.parts)
      try {
        const decision = await route(objective)
        output.message.model = { providerID: decision.provider, modelID: decision.api_model }
        await toast(`Routed to ${decision.provider}/${decision.api_model} · /veto-off`, "success")
      } catch (error) {
        await toast(`Routing unavailable; keeping current model: ${cleanError(error?.message || String(error))}`, "warning")
      }
    },

    tool: {
      veto_status: tool({
        description: "Inspect Veto automatic-routing status for this OpenCode session.",
        args: {},
        async execute(_args, context) {
          return JSON.stringify(await status(context.sessionID, context.abort))
        },
      }),
      veto_route: tool({
        description: "Ask Veto which OpenCode provider/model should handle an objective. This does not execute the objective.",
        args: { objective: tool.schema.string().min(1).max(16384) },
        async execute(args, context) {
          return JSON.stringify(await route(args.objective, context.abort))
        },
      }),
    },
  }
}

async function readBounded(stream, limit) {
  if (!stream) return ""
  const reader = stream.getReader()
  const chunks = []
  let size = 0
  while (true) {
    const { done, value } = await reader.read()
    if (done) break
    size += value.byteLength
    if (size > limit) throw new Error("Veto command output exceeds the size limit")
    chunks.push(value)
  }
  const bytes = new Uint8Array(size)
  let offset = 0
  for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength }
  return new TextDecoder().decode(bytes)
}

function boundedNumber(value, fallback, min, max) {
  const number = Number(value)
  return Number.isFinite(number) ? Math.max(min, Math.min(max, number)) : fallback
}

function cleanError(value) {
  return String(value || "").replace(/[\r\n\t]+/g, " ").trim().slice(0, 240)
}
