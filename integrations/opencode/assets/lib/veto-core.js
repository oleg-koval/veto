export const ROUTING_MARKER = "VETO_ROUTING_ORIGIN"
export const ROUTING_MARKER_VALUE = "opencode-plugin"
export const MAX_OBJECTIVE_BYTES = 16 * 1024
export const MAX_OUTPUT_BYTES = 1024 * 1024

export function objectiveFromParts(parts) {
  const text = (parts || [])
    .filter((part) => part && part.type === "text" && part.synthetic !== true && typeof part.text === "string")
    .map((part) => part.text.trim())
    .filter(Boolean)
    .join("\n")
  return new TextEncoder().encode(text).byteLength <= MAX_OBJECTIVE_BYTES ? text : ""
}

export function shouldRoute({ marker, internal, enabled, explicit, parts }) {
  if (marker === ROUTING_MARKER_VALUE || internal) return false
  if (explicit) return true
  if (!enabled) return false
  return objectiveFromParts(parts).length > 0
}

export function sessionInfo(event) {
  const info = event?.properties?.info
  if (!info || typeof info.id !== "string") return null
  return { id: info.id, internal: typeof info.title === "string" && info.title.startsWith("veto:") }
}

export function parseRouteDecision(text) {
  if (new TextEncoder().encode(text).byteLength > MAX_OUTPUT_BYTES) throw new Error("Veto output exceeds the size limit")
  const value = JSON.parse(text)
  if (!value || value.runtime !== "opencode" || !safeID(value.provider) || !safeID(value.api_model)) {
    throw new Error("Veto returned an invalid OpenCode route")
  }
  return value
}

export function routeArgs(objective, timeoutMS = 8000) {
  const seconds = Math.max(1, Math.min(30, Math.ceil(timeoutMS / 1000)))
  return ["route", "--json", "--no-resume", "--runtime", "opencode", "--timeout", `${seconds}s`, "--", objective]
}

function safeID(value) {
  return typeof value === "string" && value.length > 0 && new TextEncoder().encode(value).byteLength <= 512 && value.trim() === value && !/[\p{C}\s]/u.test(value)
}
