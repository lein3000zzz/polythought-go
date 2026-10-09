// Stand-ins for the browser APIs jsdom lacks that React Flow and Radix use, following
// React Flow's testing guide. Import this in tests that render the canvas.

class ResizeObserverStub {
  callback: ResizeObserverCallback

  constructor(callback: ResizeObserverCallback) {
    this.callback = callback
  }

  observe(target: Element) {
    const { offsetWidth: width, offsetHeight: height } = target as HTMLElement
    this.callback([{ target, contentRect: { width, height } } as ResizeObserverEntry], this)
  }

  unobserve() {}

  disconnect() {}
}

class DOMMatrixReadOnlyStub {
  m22: number

  constructor(transform?: string) {
    const scale = transform?.match(/scale\(([1-9.])\)/)?.[1]
    this.m22 = scale !== undefined ? Number(scale) : 1
  }
}

globalThis.ResizeObserver = ResizeObserverStub
globalThis.DOMMatrixReadOnly = DOMMatrixReadOnlyStub as unknown as typeof DOMMatrixReadOnly

// React Flow measures nodes with these, and hides nodes it measures as empty.
Object.defineProperties(HTMLElement.prototype, {
  offsetHeight: {
    get(this: HTMLElement) {
      return parseFloat(this.style.height) || 1
    },
  },
  offsetWidth: {
    get(this: HTMLElement) {
      return parseFloat(this.style.width) || 1
    },
  },
})

Element.prototype.hasPointerCapture = () => false
Element.prototype.releasePointerCapture = () => {}
Element.prototype.scrollIntoView = () => {}
