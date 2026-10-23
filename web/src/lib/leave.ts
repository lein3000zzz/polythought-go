/**
 * Sends the browser to another site: the consent page, back to an agent with its code.
 * A module of its own, so tests can stand in for it.
 */
export function leaveFor(url: string) {
  window.location.assign(url)
}
