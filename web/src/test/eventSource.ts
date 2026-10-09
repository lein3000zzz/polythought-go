/** Stands in for EventSource, which jsdom lacks; tests push events through it. */
export class FakeEventSource extends EventTarget {
  static readonly CONNECTING = 0
  static readonly OPEN = 1
  static readonly CLOSED = 2

  static open: FakeEventSource[] = []

  readonly url: string
  readyState = FakeEventSource.OPEN

  constructor(url: string) {
    super()
    this.url = url
    FakeEventSource.open.push(this)
  }

  close() {
    this.readyState = FakeEventSource.CLOSED
    FakeEventSource.open = FakeEventSource.open.filter((s) => s !== this)
  }

  /** Delivers an event as the server would send it. */
  emit(type: string, data: unknown) {
    this.dispatchEvent(new MessageEvent(type, { data: JSON.stringify(data) }))
  }

  /** Fails the stream the way a refused connection does: closed, for good. */
  refuse() {
    this.close()
    this.dispatchEvent(new Event('error'))
  }
}
